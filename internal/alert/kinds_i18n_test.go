package alert

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every alert kind the engine raises needs a German label (Type column), and every message key of RaiseP a German
// text, so a new kind or key cannot ship untranslated (#195).
func TestAlertKindsAndKeysHaveGerman(t *testing.T) {
	var dict strings.Builder
	files, _ := filepath.Glob("../../web/src/lib/i18n/*.js")
	if len(files) == 0 {
		t.Skip("web sources not found")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		dict.Write(b)
	}
	d := dict.String()
	src, _ := filepath.Glob("*.go")
	raise := regexp.MustCompile(`Raise(?:P)?\("([a-z_0-9]+)"`)
	key := regexp.MustCompile(`key :?= "([^"]+)"`)
	for _, f := range src {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range raise.FindAllStringSubmatch(string(b), -1) {
			if !strings.Contains(d, `"`+m[1]+`":`) {
				t.Errorf("%s: alert kind %q has no German label", f, m[1])
			}
		}
		for _, m := range key.FindAllStringSubmatch(string(b), -1) {
			if !strings.Contains(d, `"`+m[1]+`"`) {
				t.Errorf("%s: message key %q has no German text", f, m[1])
			}
		}
	}
}
