package main

import (
	"os"
	"strings"
	"testing"
)

func TestDockerPublishWorkflowSeparatesMainAndDevPackages(t *testing.T) {
	b, err := os.ReadFile(".github/workflows/docker-publish.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		`branches: [ "main", "dev" ]`,
		`github.ref_name == 'dev' || github.event.pull_request.base.ref == 'dev'`,
		`format('{0}-dev', github.repository)`,
		`type=raw,value=latest,enable=${{ github.ref == 'refs/heads/main' || github.ref == 'refs/heads/dev' }}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("workflow is missing %q", want)
		}
	}
}
