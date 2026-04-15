package docker

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamBuildOutputForwardsStreamLines(t *testing.T) {
	input := strings.Join([]string{
		`{"stream":"Step 1/3 : FROM alpine\n"}`,
		`{"stream":" ---> abc123\n"}`,
		`{"stream":"Step 2/3 : RUN echo ok\n"}`,
		`{"aux":{"ID":"sha256:deadbeef"}}`,
		`{"stream":"Successfully built abc123\n"}`,
	}, "\n")
	var sink bytes.Buffer
	if err := streamBuildOutput(strings.NewReader(input), &sink); err != nil {
		t.Fatalf("streamBuildOutput: %v", err)
	}
	got := sink.String()
	want := "Step 1/3 : FROM alpine\n ---> abc123\nStep 2/3 : RUN echo ok\nSuccessfully built abc123\n"
	if got != want {
		t.Errorf("sink = %q, want %q", got, want)
	}
}

func TestStreamBuildOutputReturnsFirstError(t *testing.T) {
	input := strings.Join([]string{
		`{"stream":"Step 1/2 : FROM alpine\n"}`,
		`{"errorDetail":{"code":1,"message":"boom"},"error":"boom"}`,
		`{"error":"second error"}`,
	}, "\n")
	var sink bytes.Buffer
	err := streamBuildOutput(strings.NewReader(input), &sink)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "boom" {
		t.Errorf("err = %q, want %q", err.Error(), "boom")
	}
	if !strings.Contains(sink.String(), "Step 1/2") {
		t.Errorf("sink missing progress line: %q", sink.String())
	}
}

func TestStreamBuildOutputNilSinkIsSilent(t *testing.T) {
	input := `{"stream":"hello\n"}`
	if err := streamBuildOutput(strings.NewReader(input), nil); err != nil {
		t.Fatalf("streamBuildOutput: %v", err)
	}
}

func TestStreamBuildOutputSplitsMultilineStream(t *testing.T) {
	input := `{"stream":"line one\nline two\n"}`
	var sink bytes.Buffer
	if err := streamBuildOutput(strings.NewReader(input), &sink); err != nil {
		t.Fatalf("streamBuildOutput: %v", err)
	}
	if sink.String() != "line one\nline two\n" {
		t.Errorf("sink = %q", sink.String())
	}
}
