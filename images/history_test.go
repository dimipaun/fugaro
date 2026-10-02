package images_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// finalStage returns the instructions of the history Dockerfile after its
// last FROM, with the FROM line first.
func finalStage(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("history/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "FROM ") {
			lines = lines[:0]
		}
		lines = append(lines, l)
	}
	return lines
}

// The history job holds admin rights on the Firebase project: its image is the
// binary in a distroless static image, running as a numeric non-root user,
// with no step that could add a shell, a package or a credential.
func TestHistoryImageIsMinimal(t *testing.T) {
	stage := finalStage(t)
	if !regexp.MustCompile(`^FROM gcr\.io/distroless/static-debian12:nonroot$`).MatchString(stage[0]) {
		t.Errorf("final stage is %q, want distroless static nonroot", stage[0])
	}
	var user string
	for _, l := range stage[1:] {
		switch kw, _, _ := strings.Cut(l, " "); kw {
		case "COPY":
			if !strings.HasPrefix(l, "COPY --from=fugaro /out/fugaro ") {
				t.Errorf("final stage copies %q; only the fugaro binary belongs in the image", l)
			}
		case "USER":
			user = strings.TrimSpace(strings.TrimPrefix(l, "USER "))
		case "ENTRYPOINT", "CMD":
		default:
			t.Errorf("final stage has %q; it may only COPY the binary, set USER, ENTRYPOINT and CMD", l)
		}
	}
	if !regexp.MustCompile(`^[1-9][0-9]*(:[1-9][0-9]*)?$`).MatchString(user) {
		t.Errorf("USER = %q, want a numeric non-root user", user)
	}
}

// build-base.sh builds any directory of images/ that has a Dockerfile, so the
// history image builds with images/build-base.sh history.
func TestHistoryImageBuildsWithBuildBase(t *testing.T) {
	if _, err := os.Stat("history/Dockerfile"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("build-base.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"$root/images/$base/Dockerfile"`) {
		t.Fatal("build-base.sh no longer builds images/<base>/Dockerfile")
	}
}
