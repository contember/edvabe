package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseMajorMinor(t *testing.T) {
	cases := []struct {
		in    string
		major int
		minor int
		ok    bool
	}{
		{"26.1.4", 26, 1, true},
		{"20.10.24", 20, 10, true},
		{"20.10.24+dfsg1", 20, 10, true},
		{"v26.1.4", 26, 1, true},
		{"19.03.15", 19, 3, true},
		{"26", 0, 0, false},
		{"", 0, 0, false},
		{"abc.def", 0, 0, false},
	}
	for _, tc := range cases {
		major, minor, ok := parseMajorMinor(tc.in)
		if ok != tc.ok || major != tc.major || minor != tc.minor {
			t.Errorf("parseMajorMinor(%q) = (%d, %d, %v), want (%d, %d, %v)",
				tc.in, major, minor, ok, tc.major, tc.minor, tc.ok)
		}
	}
}

func TestPrintResultsAllPass(t *testing.T) {
	var buf bytes.Buffer
	printResults(&buf, []checkResult{
		{name: "Docker socket", ok: true, detail: "unix:///var/run/docker.sock"},
		{name: "Docker version", ok: true, detail: "26.1.4"},
	})
	out := buf.String()
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in output, got: %q", out)
	}
	if !strings.Contains(out, "All checks passed.") {
		t.Errorf("expected summary line, got: %q", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("unexpected FAIL in all-pass output: %q", out)
	}
}

func TestPrintResultsSomeFail(t *testing.T) {
	var buf bytes.Buffer
	printResults(&buf, []checkResult{
		{name: "Docker socket", ok: true, detail: "unix:///var/run/docker.sock"},
		{name: "Port 3000 free", ok: false, detail: "address already in use"},
	})
	out := buf.String()
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL in output, got: %q", out)
	}
	if !strings.Contains(out, "1 of 2 checks failed.") {
		t.Errorf("expected failure summary, got: %q", out)
	}
}

func TestCheckPortFreeSucceedsOnUnusedPort(t *testing.T) {
	// Port 0 asks the kernel to pick an unused port; net.Listen(":0")
	// should always succeed and the returned listener is immediately
	// closed. We pass 0 to exercise the happy path without needing an
	// otherwise-free well-known port.
	r := checkPortFree(0)(nil, nil)
	if !r.ok {
		t.Fatalf("checkPortFree(0) failed: %s", r.detail)
	}
}
