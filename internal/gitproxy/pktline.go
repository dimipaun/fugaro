// Package gitproxy reads the front of a push and decides, before a single
// pack byte is forwarded, whether the run is allowed to make it: the update
// request names the refs the push would move, and everything the proxy lets
// through is bounded by what this file can see in full.
package gitproxy

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxPktLine is git's largest pkt-line, header included (LARGE_PACKET_MAX).
// A declared length above it is not a slow line but a broken one, and git
// itself refuses it.
const maxPktLine = 65520

// RefUpdate is one "old new ref" line of a receive-pack update request. Old is
// the zero id for a create and New is the zero id for a delete; the parser
// reports both, and the rules decide which of them may be forwarded.
type RefUpdate struct {
	Old string
	New string
	Ref string

	// HashLen is the width both ids were written at: 40 for a sha1
	// repository, 64 for a sha256 one. A delete is "New is all zeros at this
	// width", and a rule that does not know the width cannot ask that
	// question: a 64-zero New is a perfectly ordinary update id to a check
	// that only ever looks for 40 of them.
	HashLen int
}

// The refusals are sentinel values so a caller can name the class of a refusal
// in an audit line without carrying the request anywhere near it. None of them
// echoes a byte of what the caller sent: a pushed ref, an object id and a
// capability list are caller-controlled, and this proxy's log is not a place
// for them.
var (
	ErrUpdateRequestTooLarge = errors.New("the update request is larger than the allowed size")
	ErrPktLineMalformed      = errors.New("a pkt-line length is malformed")
	ErrPktLineOutOfRange     = errors.New("a pkt-line length is out of range")
	ErrUpdateRequestNoFlush  = errors.New("the update request does not end at a flush packet")
	ErrObjectIDWidthMixed    = errors.New("the two object ids of one update are of different widths")
	ErrPushCertRefused       = errors.New("a signed push is refused: the runner never signs one")
	ErrObjectIDInvalid       = errors.New("an object id is not 40 or 64 lowercase hex digits")
	ErrRefNameInvalid        = errors.New("a ref is not a full refs/ name")
)

// ReadUpdateRequest parses git's receive-pack update request from r up to and
// including its flush packet, and returns what was read as consumed, byte for
// byte, so the caller forwards the section unchanged before the pack.
//
// max bounds the bytes this may take from r, the flush packet included: a
// section that needs more is refused rather than read, which is what keeps a
// client from making the proxy hold a gigabyte it declares but never sends.
// Nothing past the flush is read, so the pack is still the caller's to stream.
//
// It refuses an oversized section, a malformed or out-of-range pkt-line
// length, a request that never reaches a flush, a push-cert (the runner never
// signs), an object id that is not 40 or 64 lowercase hex digits, two ids of
// one update that are not the same width, and a ref that is not a full
// refs/... name. An update request of only a flush packet - git's probe before
// it sends a large chunked push, http.postBuffer - is an empty list and no
// error, which the rules pass as a no-op.
//
// A line feed at the end of a line is tolerated and not required. Git's own
// server strips one if a client sent it, and its client sends none: a real
// push reads "old new ref\0capabilities" and then the flush, so requiring the
// byte would refuse every push that exists.
//
// A failure to read classifies as ErrUpdateRequestNoFlush whatever its cause,
// with the cause wrapped underneath: the class is what a refusal is audited
// as, and the cause is what an operator needs.
//
// The caller decides Content-Encoding before calling this: a body that is not
// identity is refused there, because nothing here decompresses and a gzip
// stream is not a pkt-line stream.
//
// On refusal the returned updates and bytes are nil: a caller that forwarded
// whatever was read before the refusal would be forwarding a request nobody
// finished checking.
func ReadUpdateRequest(r io.Reader, max int) ([]RefUpdate, []byte, error) {
	if max <= 0 {
		return nil, nil, ErrUpdateRequestTooLarge
	}
	var (
		consumed []byte
		updates  []RefUpdate
		shallow  = true
	)
	for {
		if len(consumed)+4 > max {
			return nil, nil, ErrUpdateRequestTooLarge
		}
		header := make([]byte, 4)
		if _, err := io.ReadFull(r, header); err != nil {
			return nil, nil, noFlush(err)
		}
		consumed = append(consumed, header...)
		length, ok := pktLength(header)
		if !ok {
			return nil, nil, ErrPktLineMalformed
		}
		if length == 0 {
			return updates, consumed, nil
		}
		if length < 4 || length > maxPktLine {
			return nil, nil, ErrPktLineOutOfRange
		}
		if len(consumed)+length-4 > max {
			return nil, nil, ErrUpdateRequestTooLarge
		}
		payload := make([]byte, length-4)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, nil, noFlush(err)
		}
		consumed = append(consumed, payload...)

		line := string(payload)
		if strings.HasSuffix(line, "\n") {
			line = line[:len(line)-1]
		}
		// The first line carries the capability list after a NUL, and a
		// capability may contain spaces, slashes and equals signs - everything
		// a ref may not. Splitting first is what makes "the ref" the same
		// string git will move, rather than whatever the caller glued to it.
		if i := strings.IndexByte(line, 0); i >= 0 {
			line = line[:i]
		}

		if strings.HasPrefix(line, "shallow ") {
			if !shallow {
				return nil, nil, ErrPktLineMalformed
			}
			if _, ok := objectIDWidth(strings.TrimPrefix(line, "shallow ")); !ok {
				return nil, nil, ErrObjectIDInvalid
			}
			continue
		}
		shallow = false

		if line == "push-cert" {
			return nil, nil, ErrPushCertRefused
		}
		fields := strings.SplitN(line, " ", 3)
		if len(fields) != 3 {
			return nil, nil, ErrPktLineMalformed
		}
		old, new := fields[0], fields[1]
		oldWidth, oldOK := objectIDWidth(old)
		newWidth, newOK := objectIDWidth(new)
		if !oldOK || !newOK {
			return nil, nil, ErrObjectIDInvalid
		}
		if oldWidth != newWidth {
			return nil, nil, ErrObjectIDWidthMixed
		}
		if !validRefName(fields[2]) {
			return nil, nil, ErrRefNameInvalid
		}
		updates = append(updates, RefUpdate{Old: old, New: new, Ref: fields[2], HashLen: oldWidth})
	}
}

// pktLength reads the four hex digits of a pkt-line header. Git accepts either
// case here, and so does this: the length is not part of any name the rules
// compare, and a real client that sent uppercase digits is not an attacker.
// Parsing them by hand rather than with strconv keeps "+123" - which strconv
// would happily read as 0x123 - out of the one place a header is trusted.
func pktLength(header []byte) (int, bool) {
	n := 0
	for _, c := range header {
		switch {
		case c >= '0' && c <= '9':
			n = n*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			n = n*16 + int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n = n*16 + int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}

// objectIDWidth accepts the two id widths git has - sha1's 40 and sha256's
// 64, lowercase hex only - and returns which one it read. Git writes them that
// way, and an id that is not that is a caller asking the rules to compare
// something they never see. The width is the answer to "is this a delete",
// so it is worth more than a yes.
func objectIDWidth(id string) (int, bool) {
	if len(id) != 40 && len(id) != 64 {
		return 0, false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, false
		}
	}
	return len(id), true
}

// noFlush classifies a read that could not produce the rest of the request.
// The class is what a refusal is audited as - no flush is no flush, whether
// the client disconnected, the upstream timed out or the body simply ended -
// but the cause is kept, because "the request had no flush" alone tells an
// operator nothing about a transport that is failing.
func noFlush(cause error) error {
	return fmt.Errorf("%w: %w", ErrUpdateRequestNoFlush, cause)
}

// validRefName is git-check-ref-format's rule set, because the name that
// reaches the rules must be one git would accept: a name git would refuse to
// create, but this proxy would forward, is a push whose effect nobody checked.
func validRefName(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || len(ref) <= len("refs/") {
		return false
	}
	if strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") || strings.Contains(ref, "\\") {
		return false
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c < 0x20 || c == 0x7f || c == ' ' || c == '~' || c == '^' || c == ':' ||
			c == '?' || c == '*' || c == '[' {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
