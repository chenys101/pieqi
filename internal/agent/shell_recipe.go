package agent

import (
	"fmt"
	"strings"
)

// maxTrustedRecipes 设置页可信配方上限。太大等于变相开整档 execute。
const maxTrustedRecipes = 64

// ShellRecipes 从一条 shell 命令抽出「配方指纹」列表（每段一个）。
//
// 配方是「族 + 动词」级别的归一化串，用来做会话内同类命令记忆：
//
//	go test ./internal/... -count=1  →  "go test"
//	npm run test -- --run            →  "npm run test"
//	npx vitest run x.spec.ts         →  "npx vitest"
//	git status -sb                   →  "git status"
//	Get-Content a.log                →  "get-content"
//
// 路径、旗标、参数全部剥掉——否则同一意图每次路径不同就记不住。
// 空命令 / 无法切段 → 空切片（调用方不要据此免审）。
//
// 这不是安全门禁：能不能记进 sessionRecipes 由 RecipeRememberable 另判。
func ShellRecipes(command string) []string {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, seg := range splitSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		r := recipeForSegment(seg)
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

// recipeForSegment 单段 → 配方。认不出命令名则空串。
func recipeForSegment(seg string) string {
	words := strings.Fields(seg)
	i := 0
	for i < len(words) {
		w := words[i]
		if strings.HasPrefix(w, "/dev/null") {
			i++
			continue
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			i++
			continue
		}
		if _, ok := commandWrappers[baseCommandName(w)]; ok {
			i++
			continue
		}
		break
	}
	if i >= len(words) {
		return ""
	}
	name := baseCommandName(words[i])
	args := words[i+1:]

	switch name {
	case "go":
		sub, rest, ok := goSubcommand(args)
		if !ok {
			return "go"
		}
		if sub == "mod" {
			nf := nonFlagWords(rest)
			if len(nf) > 0 {
				return "go mod " + strings.ToLower(nf[0])
			}
			return "go mod"
		}
		return "go " + strings.ToLower(sub)
	case "git":
		return gitRecipe(args)
	case "npm", "pnpm", "yarn":
		return npmLikeRecipe(name, args)
	case "npx", "pnpx":
		return npxRecipe(name, args)
	default:
		return name
	}
}

// gitRecipe：跳过 -C / -c 等全局旗标后取子命令。
func gitRecipe(args []string) string {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "-C" || a == "-c" {
			i += 2
			continue
		}
		if strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "-c=") {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return "git"
	}
	return "git " + strings.ToLower(args[i])
}

func npmLikeRecipe(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	nonFlag := nonFlagWords(args)
	switch name {
	case "npm", "pnpm":
		if len(args) >= 2 && (args[0] == "run" || args[0] == "run-script") {
			script := args[1]
			if at := strings.IndexByte(script, '@'); at > 0 {
				script = script[:at]
			}
			return name + " run " + strings.ToLower(script)
		}
		if len(nonFlag) >= 1 {
			return name + " " + strings.ToLower(nonFlag[0])
		}
	case "yarn":
		if len(args) >= 2 && (args[0] == "run" || args[0] == "run-script") {
			return "yarn run " + strings.ToLower(args[1])
		}
		if len(nonFlag) >= 1 {
			return "yarn " + strings.ToLower(nonFlag[0])
		}
	}
	return name
}

func npxRecipe(name string, args []string) string {
	nonFlag := nonFlagWords(args)
	if len(nonFlag) == 0 {
		return name
	}
	bin := strings.ToLower(nonFlag[0])
	if at := strings.IndexByte(bin, '@'); at > 0 {
		bin = bin[:at]
	}
	return name + " " + bin
}

// RecipeRememberable 该配方能否进入会话免审集合。
//
// 硬边界（与 ADR-0015 / AGENTS.md 对齐）一律 false：删除、影响远端/历史的 git、
// 装依赖、提权/进程控制、go get/install/run。其余（含仍属 L2 的 curl 等）
// 允许用户主动「同类免审」记一次。
func RecipeRememberable(recipe string) bool {
	r := strings.ToLower(strings.TrimSpace(recipe))
	if r == "" {
		return false
	}
	// 裸 npx/pnpx：未绑定具体 bin，记下去等于给任意包通行证。
	if r == "npx" || r == "pnpx" {
		return false
	}
	for _, bad := range recipeHardDenyExact {
		if r == bad {
			return false
		}
	}
	for _, bad := range recipeHardDenyPrefix {
		if r == bad || strings.HasPrefix(r, bad+" ") {
			return false
		}
	}
	if permissionReqHasUnsafeText(r) {
		return false
	}
	return true
}

// recipeHardDenyExact 整词拒绝（不会用前缀误伤更长配方）。
var recipeHardDenyExact = []string{
	"remove-item", "rm", "del", "rd", "rmdir", "clear-content", "clear-recyclebin",
	"erase", "unlink",
	"out-file", "set-content", "add-content", "new-item", "copy-item", "move-item",
	"rename-item", "tee-object", "stop-process", "start-process", "taskkill",
	"invoke-expression", "invoke-webrequest", "invoke-restmethod",
	"icacls", "takeown", "reg", "shutdown",
}

// recipeHardDenyPrefix 前缀拒绝：`git push` 拒绝自身及 `git push origin`。
var recipeHardDenyPrefix = []string{
	"git push", "git pull", "git clone",
	"git reset", "git clean", "git filter-branch", "git submodule", "git rebase",
	"npm install", "npm i", "npm add", "npm uninstall", "npm remove", "npm publish",
	"npm update", "npm ci",
	"pnpm install", "pnpm i", "pnpm add", "pnpm publish",
	"yarn install", "yarn add", "yarn publish",
	"go get", "go install", "go run", "go clean",
	"go mod tidy", "go mod download", "go mod vendor", "go mod edit",
}

// NormalizeTrustedRecipes 规范化设置页「可信配方」列表。
//
// 规则：
//   - 每项可以是完整命令（`go test ./...`）或已是配方（`go test`）；
//   - 复合命令（多段）拒绝 —— 必须一条条加，避免一次放行整条毒链；
//   - 必须通过 RecipeRememberable（硬边界不可进表）；
//   - 去重、保序；超过 maxTrustedRecipes 报错。
func NormalizeTrustedRecipes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		r, ok := normalizeOneTrustedRecipe(raw)
		if !ok {
			return nil, fmt.Errorf("invalid or forbidden recipe %q", strings.TrimSpace(raw))
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
		if len(out) > maxTrustedRecipes {
			return nil, fmt.Errorf("trusted_recipes exceeds max %d", maxTrustedRecipes)
		}
	}
	return out, nil
}

func normalizeOneTrustedRecipe(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	recipes := ShellRecipes(raw)
	var r string
	switch len(recipes) {
	case 0:
		r = strings.ToLower(raw)
	case 1:
		r = recipes[0]
	default:
		return "", false
	}
	if !RecipeRememberable(r) {
		return "", false
	}
	return r, true
}
