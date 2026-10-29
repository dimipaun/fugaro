package gitproxy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

const (
	oidA     = "1111111111111111111111111111111111111111"
	oidB     = "2222222222222222222222222222222222222222"
	oidC     = "3333333333333333333333333333333333333333"
	oidD     = "4444444444444444444444444444444444444444"
	oidE     = "5555555555555555555555555555555555555555"
	oidF     = "6666666666666666666666666666666666666666"
	oidLower = "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	oidUpper = "ABCDEFABCDEFABCDEFABCDEFABCDEFABCDEFABCD"
	zeroOID  = "0000000000000000000000000000000000000000"
)

func pkt(payload string) string {
	if len(payload) > maxPktLine-4 {
		panic("test payload too long for one pkt-line")
	}
	return fmt.Sprintf("%04x", len(payload)+4) + payload
}

func TestReadUpdateRequest(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []RefUpdate
	}{
		{
			name: "documented example, one update, no capabilities",
			in:   pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main"}},
		},
		{
			name: "one update carrying capabilities after a NUL",
			in:   pkt(oidA+" "+oidB+" refs/heads/main\x00report-status side-band-64k agent=git/2.42.0\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main"}},
		},
		{
			name: "a literal length prefix, as the documentation prints one",
			in:   "0035" + "shallow " + oidA + "\n" + pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main"}},
		},
		{
			name: "shallow lines come first",
			in: pkt("shallow "+oidC+"\n") + pkt("shallow "+oidD+"\n") +
				pkt(oidA+" "+oidB+" refs/heads/main\x00report-status\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main"}},
		},
		{
			name: "shallow of the zero id is a line like any other",
			in:   pkt("shallow "+zeroOID+"\n") + pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main"}},
		},
		{
			name: "several updates",
			in: pkt(oidA+" "+oidB+" refs/heads/main\x00report-status\n") +
				pkt(oidC+" "+oidD+" refs/heads/other\n") +
				pkt(oidE+" "+oidF+" refs/heads/fugaro/20261029-225717-8499\n") + "0000",
			want: []RefUpdate{
				{Old: oidA, New: oidB, Ref: "refs/heads/main"},
				{Old: oidC, New: oidD, Ref: "refs/heads/other"},
				{Old: oidE, New: oidF, Ref: "refs/heads/fugaro/20261029-225717-8499"},
			},
		},
		{
			name: "a delete is parsed: rules decide it, not the parser",
			in:   pkt(oidA+" "+zeroOID+" refs/heads/gone\n") + "0000",
			want: []RefUpdate{{Old: oidA, New: zeroOID, Ref: "refs/heads/gone"}},
		},
		{
			name: "a create is parsed",
			in:   pkt(zeroOID+" "+oidB+" refs/heads/new\n") + "0000",
			want: []RefUpdate{{Old: zeroOID, New: oidB, Ref: "refs/heads/new"}},
		},
		{
			name: "sha256 object ids",
			in:   pkt(strings.Repeat("a", 64)+" "+strings.Repeat("b", 64)+" refs/heads/main\n") + "0000",
			want: []RefUpdate{
				{Old: strings.Repeat("a", 64), New: strings.Repeat("b", 64), Ref: "refs/heads/main"},
			},
		},
		{
			name: "the probe before a large push is an empty list",
			in:   "0000",
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmds, consumed, err := ReadUpdateRequest(strings.NewReader(tc.in), 1<<20)
			if err != nil {
				t.Fatalf("ReadUpdateRequest: %v", err)
			}
			if len(cmds) != len(tc.want) {
				t.Fatalf("got %d updates, want %d", len(cmds), len(tc.want))
			}
			for i := range cmds {
				if cmds[i] != tc.want[i] {
					t.Errorf("update %d: got %+v, want %+v", i, cmds[i], tc.want[i])
				}
			}
			if string(consumed) != tc.in {
				t.Errorf("consumed %q, want the whole request %q", consumed, tc.in)
			}
		})
	}
}

func TestReadUpdateRequestRefusals(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want error
	}{
		{
			name: "a request that never reaches its flush",
			in:   pkt(oidA + " " + oidB + " refs/heads/main\n"),
			want: ErrUpdateRequestNoFlush,
		},
		{
			name: "a header cut short by EOF",
			in:   "00",
			want: ErrUpdateRequestNoFlush,
		},
		{
			name: "a payload cut short by EOF",
			in:   "0100" + oidA,
			want: ErrUpdateRequestNoFlush,
		},
		{
			name: "a length that is not hex",
			in:   "00zz" + "0000",
			want: ErrPktLineMalformed,
		},
		{
			name: "a length that is not hex but parses as a signed number",
			in:   "+123" + "0000",
			want: ErrPktLineMalformed,
		},
		{
			name: "the protocol v2 delimiter, which is not an update request",
			in:   "0001" + "0000",
			want: ErrPktLineOutOfRange,
		},
		{
			name: "the protocol v2 response end",
			in:   "0002" + "0000",
			want: ErrPktLineOutOfRange,
		},
		{
			name: "a length below the four header bytes",
			in:   "0003" + "0000",
			want: ErrPktLineOutOfRange,
		},
		{
			name: "a length past the largest pkt-line",
			in:   "ffff" + "0000",
			want: ErrPktLineOutOfRange,
		},
		{
			name: "a legal length whose payload has no line feed",
			in:   "fff0" + strings.Repeat("x", maxPktLine-4),
			max:  1 << 20,
			want: ErrPktLineMalformed,
		},
		{
			name: "a line with no line feed",
			in:   pkt(oidA+" "+oidB+" refs/heads/main") + "0000",
			want: ErrPktLineMalformed,
		},
		{
			name: "a command with two fields",
			in:   pkt(oidA+" "+oidB+"\n") + "0000",
			want: ErrPktLineMalformed,
		},
		{
			name: "a command with four fields",
			in:   pkt(oidA+" "+oidB+" refs/heads/main extra\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "an uppercase object id",
			in:   pkt(oidUpper+" "+oidB+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "an object id one hex digit short",
			in:   pkt(oidA[:39]+" "+oidB+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "an object id one hex digit long",
			in:   pkt(oidA+"0 "+oidB+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "an object id that is not hex",
			in:   pkt(strings.Repeat("g", 40)+" "+oidB+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "the new object id is refused the same way",
			in:   pkt(oidA+" "+oidUpper+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "a shallow line with no object id",
			in:   pkt("shallow notoid\n") + pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000",
			want: ErrObjectIDInvalid,
		},
		{
			name: "a shallow line after the commands began",
			in:   pkt(oidA+" "+oidB+" refs/heads/main\n") + pkt("shallow "+oidC+"\n") + "0000",
			want: ErrPktLineMalformed,
		},
		{
			name: "a ref that is not a full name",
			in:   pkt(oidA+" "+oidB+" main\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a bare refs/ prefix",
			in:   pkt(oidA+" "+oidB+" refs/\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref with a double dot",
			in:   pkt(oidA+" "+oidB+" refs/heads/a..b\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref component ending in .lock",
			in:   pkt(oidA+" "+oidB+" refs/heads/x.lock\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref component starting with a dot",
			in:   pkt(oidA+" "+oidB+" refs/.hidden\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref with a colon",
			in:   pkt(oidA+" "+oidB+" refs/heads/a:b\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref with a backslash",
			in:   pkt(oidA+" "+oidB+" refs/heads/a\\b\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a ref with a curl brace sequence",
			in:   pkt(oidA+" "+oidB+" refs/heads/a@{b\n") + "0000",
			want: ErrRefNameInvalid,
		},
		{
			name: "a push-cert, which the runner never signs",
			in:   pkt("push-cert\x00report-status\n") + pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000",
			want: ErrPushCertRefused,
		},
		{
			name: "a push-cert after shallow lines",
			in:   pkt("shallow "+oidC+"\n") + pkt("push-cert\x00report-status\n") + "0000",
			want: ErrPushCertRefused,
		},
		{
			name: "a section larger than the bound",
			in:   pkt(oidA+" "+oidB+" refs/heads/main\n") + pkt(oidC+" "+oidD+" refs/heads/main\n") + "0000",
			max:  64,
			want: ErrUpdateRequestTooLarge,
		},
		{
			name: "a bound of zero",
			in:   "0000",
			max:  -1,
			want: ErrUpdateRequestTooLarge,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			max := tc.max
			if max == 0 {
				max = 1 << 20
			}
			cmds, consumed, err := ReadUpdateRequest(strings.NewReader(tc.in), max)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got error %v, want %v", err, tc.want)
			}
			if cmds != nil || consumed != nil {
				t.Errorf("a refusal returned cmds %v and consumed %q, want neither", cmds, consumed)
			}
		})
	}
}

// TestReadUpdateRequestBoundedRead is the reason the bound exists: a push that
// declares far more than a gigabyte of update request must be refused after at
// most max bytes, never after the whole stream. The stream below will keep
// producing well-formed pkt-lines for a gigabyte if it is asked for them.
func TestReadUpdateRequestBoundedRead(t *testing.T) {
	const max = 64 << 10
	line := pkt(oidA + " " + oidB + " refs/heads/main\n")
	r := &endlessPktLines{line: line, ceiling: 1 << 30, guard: max}

	cmds, consumed, err := ReadUpdateRequest(r, max)
	if !errors.Is(err, ErrUpdateRequestTooLarge) {
		t.Fatalf("got error %v, want %v (read %d bytes of an endless stream)", err, ErrUpdateRequestTooLarge, r.served)
	}
	if cmds != nil || consumed != nil {
		t.Errorf("a refusal returned cmds %v and consumed %q, want neither", cmds, consumed)
	}
	if r.served > max {
		t.Errorf("read %d bytes, want no more than the %d-byte bound", r.served, max)
	}
	if r.requested > max {
		t.Errorf("asked for %d bytes, want no more than the %d-byte bound", r.requested, max)
	}
}

// endlessPktLines hands out the same pkt-line forever, up to ceiling bytes - a
// section that declares a gigabyte. It refuses to hand out more than guard
// bytes, so an unbounded reader fails at once instead of allocating that
// gigabyte; requested counts the bytes asked for, including a read it refused.
type endlessPktLines struct {
	line      string
	off       int
	served    int
	requested int
	ceiling   int
	guard     int
}

func (r *endlessPktLines) Read(p []byte) (int, error) {
	r.requested += len(p)
	if r.served+len(p) > r.guard {
		return 0, fmt.Errorf("test reader: asked for bytes past the %d-byte guard", r.guard)
	}
	if r.served >= r.ceiling {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) {
		c := copy(p[n:], r.line[r.off:])
		n += c
		r.off = (r.off + c) % len(r.line)
	}
	r.served += n
	return n, nil
}

func TestPushCertRefused(t *testing.T) {
	in := pkt("push-cert\x00report-status\n") +
		pkt("certificate version 0.1\n") +
		pkt("pusher person-1 <person-1@example.com> 1700000000 +0000\n") +
		"0000"
	cmds, consumed, err := ReadUpdateRequest(strings.NewReader(in), 1<<20)
	if !errors.Is(err, ErrPushCertRefused) {
		t.Fatalf("got error %v, want %v", err, ErrPushCertRefused)
	}
	if cmds != nil || consumed != nil {
		t.Errorf("a refusal returned cmds %v and consumed %q, want neither", cmds, consumed)
	}
}

func TestRefComparedAfterNUL(t *testing.T) {
	cmds, _, err := ReadUpdateRequest(
		strings.NewReader(pkt(oidA+" "+oidB+" refs/heads/fugaro/x\x00report-status\n")+"0000"), 1<<20)
	if err != nil {
		t.Fatalf("ReadUpdateRequest: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d updates, want 1", len(cmds))
	}
	if cmds[0].Ref != "refs/heads/fugaro/x" {
		t.Errorf("ref %q, want %q: the capabilities after the NUL are not part of the ref", cmds[0].Ref, "refs/heads/fugaro/x")
	}
}

func TestUpdateRequestFlushOnlyProbe(t *testing.T) {
	r := strings.NewReader("0000" + "PACK")
	cmds, consumed, err := ReadUpdateRequest(r, 1<<20)
	if err != nil {
		t.Fatalf("ReadUpdateRequest: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("got %d updates, want none", len(cmds))
	}
	if string(consumed) != "0000" {
		t.Errorf("consumed %q, want the flush packet alone", consumed)
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the rest: %v", err)
	}
	if string(rest) != "PACK" {
		t.Errorf("the parser left %q unread, want the pack %q", rest, "PACK")
	}
}

func TestReadUpdateRequestErrorsNeverEchoInput(t *testing.T) {
	const canary = "canary-canary-canary"
	bad := []string{
		pkt(oidA+" "+oidB+" refs/heads/"+canary+".lock\n") + "0000",
		pkt(oidA+" "+oidB+" refs/heads/main\n") + pkt(oidLower+" "+oidB+" refs/heads/"+canary+"..x\n") + "0000",
		canary[:4] + " " + oidB + " refs/heads/main\n0000",
		pkt(oidA+" "+oidB+" refs/heads/main") + canary,
		pkt("push-cert\x00"+canary+"\n") + "0000",
		pkt(oidA+" "+oidB+" "+canary+"\n") + "0000",
	}
	for _, in := range bad {
		cmds, consumed, err := ReadUpdateRequest(strings.NewReader(in), 1<<20)
		if err == nil {
			t.Fatalf("input %q was accepted: %v %q", in, cmds, consumed)
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), in) {
			t.Errorf("error %q echoes the caller's bytes", err)
		}
	}
}

func FuzzReadUpdateRequest(f *testing.F) {
	f.Add([]byte("0000"))
	f.Add([]byte(pkt(oidA+" "+oidB+" refs/heads/main\x00report-status\n") + "0000"))
	f.Add([]byte(pkt("shallow "+oidC+"\n") + pkt(oidA+" "+oidB+" refs/heads/main\n") + "0000"))
	f.Add([]byte(pkt("push-cert\x00report-status\n") + "0000"))
	f.Add([]byte("ffff" + strings.Repeat("x", 32)))
	f.Fuzz(func(t *testing.T, data []byte) {
		r := &countingReader{data: data}
		cmds, consumed, err := ReadUpdateRequest(r, len(data)+4)
		if len(consumed) > len(data) {
			t.Fatalf("returned %d consumed bytes of %d read", len(consumed), len(data))
		}
		if !bytes.HasPrefix(data, consumed) {
			t.Fatalf("consumed %q is not a prefix of the input", consumed)
		}
		if err != nil {
			if cmds != nil || consumed != nil {
				t.Fatalf("a refusal returned cmds %v and consumed %q, want neither", cmds, consumed)
			}
			return
		}
		if !bytes.HasSuffix(consumed, []byte("0000")) {
			t.Fatalf("a request was accepted without ending at its flush: %q", consumed)
		}
		if r.requested != len(consumed) {
			t.Fatalf("asked the reader for %d bytes but consumed %d: it read past the flush", r.requested, len(consumed))
		}
	})
}

// countingReader hands out data and refuses to read past its end, and counts
// the bytes asked for, whether the read succeeded or not: asking for a byte
// that is not there is exactly the read past the flush the parser must not do.
type countingReader struct {
	data      []byte
	off       int
	requested int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.requested += len(p)
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
