package builder

import (
	"regexp"
	"strings"
	"testing"

	"github.com/contember/edvabe/internal/template"
)

// stripCacheBustARG removes any ARG EDVABE_CACHE_BUST_<n>=<hex> lines
// so that translator tests don't have to contain random bytes. We
// assert on the cache bust lines' *shape* separately.
var cacheBustLine = regexp.MustCompile(`(?m)^ARG EDVABE_CACHE_BUST_\d+=[0-9a-f]+\n`)

func translateFixture(t *testing.T, in Input) string {
	t.Helper()
	out, err := Translate(in)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	return out.Dockerfile
}

func envdTail() string {
	return "COPY --from=edvabe/envd-source:latest /usr/local/bin/envd /usr/local/bin/envd\n" +
		"COPY --from=edvabe/envd-source:latest /usr/local/bin/edvabe-init /usr/local/bin/edvabe-init\n" +
		"CMD [\"/usr/local/bin/edvabe-init\"]\n"
}

func TestTranslateFromImageOnly(t *testing.T) {
	got := translateFixture(t, Input{FromImage: "oven/bun:slim"})
	want := "FROM oven/bun:slim\n" + envdTail()
	if got != want {
		t.Fatalf("mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTranslateFromTemplate(t *testing.T) {
	got := translateFixture(t, Input{FromTemplateImage: "edvabe/user-tpl_abc:latest"})
	want := "FROM edvabe/user-tpl_abc:latest\n" + envdTail()
	if got != want {
		t.Fatalf("mismatch:\ngot:\n%s", got)
	}
}

func TestTranslateRejectsBothFroms(t *testing.T) {
	if _, err := Translate(Input{FromImage: "a", FromTemplateImage: "b"}); err == nil {
		t.Fatal("expected error when both FromImage and FromTemplateImage are set")
	}
}

func TestTranslateRejectsNeitherFrom(t *testing.T) {
	if _, err := Translate(Input{}); err == nil {
		t.Fatal("expected error when neither FromImage nor FromTemplateImage is set")
	}
}

func TestTranslateRunStep(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "debian:latest",
		Steps: []template.Step{
			{Type: "RUN", Args: []string{"echo hello"}},
		},
	})
	want := "FROM debian:latest\nRUN echo hello\n" + envdTail()
	if got != want {
		t.Fatalf("mismatch:\n%s", got)
	}
}

func TestTranslateRunWithUserSandwich(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "debian:latest",
		Steps: []template.Step{
			{Type: "USER", Args: []string{"user"}},
			{Type: "RUN", Args: []string{"apt-get update && apt-get install -y curl", "root"}},
			{Type: "RUN", Args: []string{"whoami"}},
		},
	})
	want := "FROM debian:latest\n" +
		"USER user\n" +
		"USER root\n" +
		"RUN apt-get update && apt-get install -y curl\n" +
		"USER user\n" +
		"RUN whoami\n" +
		envdTail()
	if got != want {
		t.Fatalf("mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTranslateRunUserMatchesCurrent(t *testing.T) {
	// When the RUN's override user is the same as currentUser, no
	// sandwich should be emitted.
	got := translateFixture(t, Input{
		FromImage: "debian:latest",
		Steps: []template.Step{
			{Type: "USER", Args: []string{"root"}},
			{Type: "RUN", Args: []string{"apt-get update", "root"}},
		},
	})
	// Expect a single USER root, then RUN (no second USER).
	if strings.Count(got, "USER root\n") != 1 {
		t.Fatalf("expected exactly one USER root line, got:\n%s", got)
	}
}

func TestTranslateCopyStep(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{
				Type:      "COPY",
				Args:      []string{"template", "/home/user/template"},
				FilesHash: "0000000000000000000000000000000000000000000000000000000000000000",
			},
		},
	})
	wantLine := "COPY ctx/0000000000000000000000000000000000000000000000000000000000000000/template /home/user/template\n"
	if !strings.Contains(got, wantLine) {
		t.Fatalf("missing COPY line:\n%s", got)
	}
}

func TestTranslateCopyWithChownAndMode(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{
				Type: "COPY",
				Args: []string{
					"e2b-chrome-start.sh",
					"/home/user/.chrome-start.sh",
					"user:user",
					"0755",
				},
				FilesHash: "abcdef0000000000000000000000000000000000000000000000000000000001",
			},
		},
	})
	wantLine := "COPY --chown=user:user --chmod=0755 ctx/abcdef0000000000000000000000000000000000000000000000000000000001/e2b-chrome-start.sh /home/user/.chrome-start.sh\n"
	if !strings.Contains(got, wantLine) {
		t.Fatalf("expected COPY line:\nwant: %sgot:\n%s", wantLine, got)
	}
}

func TestTranslateCopyRequiresFilesHash(t *testing.T) {
	_, err := Translate(Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "COPY", Args: []string{"a", "b"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "filesHash") {
		t.Fatalf("expected filesHash error, got %v", err)
	}
}

func TestTranslateCopyCollectsUniqueHashes(t *testing.T) {
	out, err := Translate(Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "COPY", Args: []string{"a", "/a"}, FilesHash: "1111111111111111111111111111111111111111111111111111111111111111"},
			{Type: "COPY", Args: []string{"b", "/b"}, FilesHash: "2222222222222222222222222222222222222222222222222222222222222222"},
			{Type: "COPY", Args: []string{"a", "/a2"}, FilesHash: "1111111111111111111111111111111111111111111111111111111111111111"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.RequiredFileHashes) != 2 {
		t.Fatalf("expected 2 unique hashes, got %d: %v", len(out.RequiredFileHashes), out.RequiredFileHashes)
	}
	if out.RequiredFileHashes[0][0] != '1' || out.RequiredFileHashes[1][0] != '2' {
		t.Fatalf("unexpected order: %v", out.RequiredFileHashes)
	}
}

func TestTranslateWorkdirStep(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "WORKDIR", Args: []string{"/home/user"}},
		},
	})
	if !strings.Contains(got, "WORKDIR /home/user\n") {
		t.Fatalf("missing WORKDIR:\n%s", got)
	}
}

func TestTranslateUserStep(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "USER", Args: []string{"webmaster"}},
		},
	})
	if !strings.Contains(got, "USER webmaster\n") {
		t.Fatalf("missing USER:\n%s", got)
	}
}

func TestTranslateEnvStep(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "ENV", Args: []string{"FOO", "bar", "BAZ", "with space"}},
		},
	})
	if !strings.Contains(got, "ENV FOO=bar BAZ=\"with space\"\n") {
		t.Fatalf("missing ENV line:\n%s", got)
	}
}

func TestTranslateEnvRejectsOddArgs(t *testing.T) {
	_, err := Translate(Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "ENV", Args: []string{"LONELY"}},
		},
	})
	if err == nil {
		t.Fatal("expected error for odd-length ENV args")
	}
}

func TestTranslateUnknownStepType(t *testing.T) {
	_, err := Translate(Input{
		FromImage: "alpine:latest",
		Steps:     []template.Step{{Type: "SHOUT", Args: []string{"hi"}}},
	})
	if err == nil {
		t.Fatal("expected error for unknown step type")
	}
}

func TestTranslateForceEmitsCacheBust(t *testing.T) {
	got := translateFixture(t, Input{
		FromImage: "alpine:latest",
		Steps: []template.Step{
			{Type: "RUN", Args: []string{"echo once"}, Force: true},
		},
	})
	if !cacheBustLine.MatchString(got) {
		t.Fatalf("expected cache bust line, got:\n%s", got)
	}
	// Stripping the cache bust must give us the deterministic output.
	stripped := cacheBustLine.ReplaceAllString(got, "")
	want := "FROM alpine:latest\nRUN echo once\n" + envdTail()
	if stripped != want {
		t.Fatalf("post-strip mismatch:\nwant:\n%s\ngot:\n%s", want, stripped)
	}
}

func TestTranslateWebmasterChromeShape(t *testing.T) {
	// A condensed approximation of the webmaster chrome template
	// shape, covering aptInstall (RUN as root), copy with chown/mode,
	// workdir, user switch, and a ready user. Exercises the sandwich
	// logic and COPY flag handling in one fixture.
	out, err := Translate(Input{
		FromImage: "oven/bun:slim",
		Steps: []template.Step{
			{Type: "USER", Args: []string{"root"}},
			{Type: "RUN", Args: []string{"apt-get update && apt-get install -y chromium curl", "root"}},
			{Type: "RUN", Args: []string{"fc-cache -f -v"}},
			{
				Type:      "COPY",
				Args:      []string{"e2b-chrome-start.sh", "/home/user/.chrome-start.sh", "user:user", "0755"},
				FilesHash: "dead00000000000000000000000000000000000000000000000000000000beef",
			},
			{Type: "WORKDIR", Args: []string{"/home/user"}},
			{Type: "USER", Args: []string{"user"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	df := out.Dockerfile
	for _, frag := range []string{
		"FROM oven/bun:slim\n",
		"USER root\n",
		"RUN apt-get update && apt-get install -y chromium curl\n",
		"RUN fc-cache -f -v\n",
		"COPY --chown=user:user --chmod=0755 ctx/dead00000000000000000000000000000000000000000000000000000000beef/e2b-chrome-start.sh /home/user/.chrome-start.sh\n",
		"WORKDIR /home/user\n",
		"USER user\n",
		"COPY --from=edvabe/envd-source:latest /usr/local/bin/envd /usr/local/bin/envd\n",
		"CMD [\"/usr/local/bin/edvabe-init\"]\n",
	} {
		if !strings.Contains(df, frag) {
			t.Errorf("missing fragment %q in:\n%s", frag, df)
		}
	}
	if len(out.RequiredFileHashes) != 1 {
		t.Fatalf("expected 1 file hash, got %v", out.RequiredFileHashes)
	}
}

func TestQuoteEnvValue(t *testing.T) {
	cases := map[string]string{
		"simple":      "simple",
		"":            `""`,
		"with space":  `"with space"`,
		`with"quote`:  `"with\"quote"`,
		`with\back`:   `"with\\back"`,
		"has$dollar":  `"has$dollar"`,
		"underscore_": "underscore_",
	}
	for in, want := range cases {
		if got := quoteEnvValue(in); got != want {
			t.Errorf("quoteEnvValue(%q) = %q, want %q", in, got, want)
		}
	}
}
