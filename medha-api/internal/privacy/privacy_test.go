package privacy

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilter_RedactsKnownSecretFormats(t *testing.T) {
	// Every line of the corpus must be redacted, with zero leaks.
	f, err := os.Open(filepath.Join("testdata", "secrets_corpus.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 4096)
	scanner.Buffer(buf, 1024*1024)
	// Read whole file to handle multi-line PEM blocks correctly.
	var all strings.Builder
	for scanner.Scan() {
		all.WriteString(scanner.Text())
		all.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	out, res := Filter(all.String())
	if !res.HadSecrets {
		t.Fatal("HadSecrets should be true on corpus")
	}

	// Spot-check known secrets do not appear in the output.
	mustNotLeak := []string{
		"sk-ant-api03-AbCdEf",
		"sk-proj-AbCdEf",
		"ghp_aBcDe",
		"AKIAIOSFODNN7EXAMPLE",
		"wJalrXUtnFEMI/K7MDENG",
		"xoxb-1234567890",
		"AIzaSyD-Ab123cdEf",
		"hunter2-very-secret",
		"SuperSecret123!",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIi",
		"BEGIN RSA PRIVATE KEY",
	}
	for _, leak := range mustNotLeak {
		if strings.Contains(out, leak) {
			t.Errorf("leak: %q still present in output", leak)
		}
	}
}

func TestFilter_StripsPrivateBlock(t *testing.T) {
	in := "before <private>this should\nnever be stored</private> after"
	out, _ := Filter(in)
	if strings.Contains(out, "never be stored") {
		t.Errorf("<private> block not removed: %q", out)
	}
	if !strings.HasPrefix(out, "before ") || !strings.HasSuffix(out, " after") {
		t.Errorf("surrounding text mangled: %q", out)
	}
}

func TestFilter_PrivateBlockEatsSecrets(t *testing.T) {
	// A secret inside a <private> block must be removed by the block strip,
	// not just redacted — the entire block goes.
	in := "<private>ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789</private>"
	out, _ := Filter(in)
	if out != "" {
		t.Errorf("private block should be removed entirely, got %q", out)
	}
}

func TestFilter_StripsANSI(t *testing.T) {
	in := "\x1b[31mhello\x1b[0m world"
	out, _ := Filter(in)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI not stripped: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("visible text lost: %q", out)
	}
}

func TestFilter_ANSIDoesNotHideSecret(t *testing.T) {
	// A secret wrapped in colour codes still must be redacted.
	in := "\x1b[31msk-ant-api03-ABCDEFghijklmnopqrstuvwxyz0123456789\x1b[0m"
	out, res := Filter(in)
	if !res.HadSecrets {
		t.Fatal("secret inside ANSI should be detected")
	}
	if strings.Contains(out, "sk-ant-api03-") {
		t.Errorf("secret leaked through ANSI: %q", out)
	}
}

func TestFilter_EmptyAndClean(t *testing.T) {
	out, res := Filter("")
	if out != "" || res.HadSecrets {
		t.Errorf("empty input got %q, %+v", out, res)
	}
	out, res = Filter("clean output with no secrets")
	if out != "clean output with no secrets" || res.HadSecrets {
		t.Errorf("clean input got %q, %+v", out, res)
	}
}

func TestFilter_HitPatternsRecorded(t *testing.T) {
	_, res := Filter("password = secret123 and ghp_abcdefghijklmnopqrstuvwxyz0123456")
	if !res.HadSecrets {
		t.Fatal("HadSecrets should be true")
	}
	names := strings.Join(res.HitPatterns, ",")
	if !strings.Contains(names, "github_token") || !strings.Contains(names, "generic_key_value") {
		t.Errorf("missing pattern names: %v", res.HitPatterns)
	}
}

func TestFilter_Performance(t *testing.T) {
	// Filtering a typical 4 KB payload must add < 5ms (FR-10 NFR perf budget).
	const size = 4 * 1024
	payload := strings.Repeat("the quick brown fox jumps over the lazy dog ", size/40)
	start := time.Now()
	for i := 0; i < 20; i++ {
		_, _ = Filter(payload)
	}
	avg := time.Since(start) / 20
	if avg > 5*time.Millisecond {
		t.Errorf("Filter avg = %v > 5ms budget", avg)
	}
}

func TestFilterBytes_PassthroughWhenClean(t *testing.T) {
	in := []byte("clean output")
	out, res := FilterBytes(in)
	if res.HadSecrets {
		t.Error("clean input flagged")
	}
	// Should reuse the same backing array when nothing changed.
	if &in[0] != &out[0] {
		t.Logf("FilterBytes copied (acceptable, but inefficient)")
	}
}
