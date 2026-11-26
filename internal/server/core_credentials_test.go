package server

import (
	"os"
	"testing"
)

func TestManagedControllerReadsYAMLSecretWithoutEnvironment(t *testing.T) {
	p := testCorePanel(t)
	p.secret = ""
	t.Setenv("MIHOMO_SECRET", "obsolete-value-must-not-be-used")
	for _, secret := range []string{"first-key", "changed-key", ""} {
		if err := os.WriteFile(p.path, []byte("secret: "+secret+"\nrules: [\"MATCH,DIRECT\"]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if actual := p.connection().Secret; actual != secret {
			t.Fatalf("got controller secret %q, want YAML value %q", actual, secret)
		}
	}
}
