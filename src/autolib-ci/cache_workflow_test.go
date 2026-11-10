package main

import (
	"strings"
	"testing"
)

func TestCICacheWorkflow(t *testing.T) {
	document := mustReadText(t, "../../.github/workflows/build-platform.yml")
	concurrency := workflowSection(t, document, "concurrency:")
	for _, text := range []string{"group: ${{ github.repository }}-autolib-release", "cancel-in-progress: false", "queue: max"} {
		if !strings.Contains(concurrency, text) {
			t.Fatalf("missing cache queue policy %s", text)
		}
	}
	plan := workflowSection(t, document, "  plan:")
	prepare := workflowSection(t, plan, "      - name: Prepare persistent cache")
	for _, text := range []string{"GITHUB_TOKEN: ${{ github.token }}", "GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}", "GITEE_REPOSITORY: ${{ vars.GITEE_REPOSITORY }}", " cache-prepare ", "--repository \"$GITHUB_REPOSITORY\"", "--plan build/ci-input/plan.json", "--run-identity build/ci-input/run.json", "--output build/ci-input/cache.json"} {
		if !strings.Contains(prepare, text) {
			t.Fatalf("missing shared cache preparation: %s", text)
		}
	}
	for _, id := range []string{"plan", "linux", "windows", "darwin"} {
		block := workflowSection(t, document, "  "+id+":")
		permissions := workflowSection(t, block, "    permissions:")
		if !strings.Contains(permissions, "contents: write") {
			t.Fatalf("cache writer %s lacks contents permission", id)
		}
		if id == "plan" {
			continue
		}
		if workflowField(t, block, "needs") != "plan" {
			t.Fatal("platform started without cache preparation", id)
		}
		for _, name := range []string{"Prepare platform", "Produce dependency layers", "Verify selected work"} {
			step := workflowSection(t, block, "      - name: "+name)
			if !strings.Contains(step, "--cache build/ci-input/cache.json") || !strings.Contains(step, "GITHUB_TOKEN: ${{ github.token }}") || !strings.Contains(step, "GITEE_TOKEN: ${{ secrets.GITEE_TOKEN }}") || !strings.Contains(step, "GITEE_REPOSITORY: ${{ vars.GITEE_REPOSITORY }}") {
				t.Fatalf("%s %s lacks explicit cache or scoped token", id, name)
			}
		}
		environment := workflowSection(t, block, "      - name: Prepare native environment and tools")
		if strings.Contains(environment, "GITHUB_TOKEN:") || strings.Contains(environment, "GITEE_TOKEN:") {
			t.Fatal("tool bootstrap received cache credential")
		}
	}
	for _, forbidden := range []string{"ci-package-", "transport.tar", "continue-on-error:"} {
		if strings.Contains(document, forbidden) {
			t.Fatal("cache build contains", forbidden)
		}
	}
	publish := mustReadText(t, "../../.github/workflows/publish-artifacts.yml")
	for _, text := range []string{"workflow_run:", "queue: max", "group: ${{ github.repository }}-autolib-release"} {
		if !strings.Contains(publish, text) {
			t.Fatal("missing independent sync policy", text)
		}
	}
}
