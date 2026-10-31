package agent

import (
	"reflect"
	"testing"
)

func TestShellRecipes(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"go test ./internal/...", []string{"go test"}},
		{"go -C web build ./...", []string{"go build"}},
		{"go mod graph", []string{"go mod graph"}},
		{"npm run test -- --run", []string{"npm run test"}},
		{"npx vitest run x.spec.ts", []string{"npx vitest"}},
		{"git -C D:\\repo status -sb", []string{"git status"}},
		{"Get-Content a.log | Select-Object -First 5", []string{"get-content", "select-object"}},
		{"cd web; npm run build", []string{"cd", "npm run build"}},
		{"curl -s http://127.0.0.1:3000/ready", []string{"curl"}},
		{"", nil},
	}
	for _, c := range cases {
		got := ShellRecipes(c.cmd)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q → %#v, want %#v", c.cmd, got, c.want)
		}
	}
}

func TestRecipeRememberable(t *testing.T) {
	allow := []string{"curl", "go test", "go build", "npx vitest", "npm run test", "git status", "netstat"}
	deny := []string{
		"remove-item", "rm", "git push", "git reset", "npm install", "npm i",
		"go run", "go get", "go install", "go mod tidy", "npx", "",
	}
	for _, r := range allow {
		if !RecipeRememberable(r) {
			t.Errorf("%q should be rememberable", r)
		}
	}
	for _, r := range deny {
		if RecipeRememberable(r) {
			t.Errorf("%q must NOT be rememberable", r)
		}
	}
}

func TestNormalizeTrustedRecipes(t *testing.T) {
	got, err := NormalizeTrustedRecipes([]string{
		"go test ./internal/...",
		"curl",
		"go test", // 去重
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "go test" || got[1] != "curl" {
		t.Fatalf("got %#v", got)
	}
	if _, err := NormalizeTrustedRecipes([]string{"git push origin main"}); err == nil {
		t.Fatal("git push must be rejected")
	}
	if _, err := NormalizeTrustedRecipes([]string{"cd web; go test"}); err == nil {
		t.Fatal("compound must be rejected")
	}
}
