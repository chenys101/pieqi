// provider_cache_test.go：**启动**不得作废模型清单的磁盘缓存。
//
// 回归（2026-10-22 实测踩到）：ConfigureACPProviders 在启动时被第一次调用，那时
// 内部"旧配置"是空的，于是每个 agent 都被判成"探测身份变了"并调 InvalidateAgentModels
// ——而它连磁盘档一起删。结果是进程刚起来就把上一进程存下的清单删掉，
// 磁盘缓存唯一的用途（跨重启）当场作废：实测重启后首个请求仍然要等 **7.0s**。
//
// 判据：**启动**只建立基线，**运行期变更**才作废。两者必须分开，否则要么每次启动
// 白删缓存，要么改了 spawn 路径后一整天还拿着旧清单。
package agent

import (
	"os"
	"testing"

	"pieqi/internal/config"
)

// resetProviderForTest 让包级 provider 表回到"从未装配"的启动前状态。
func resetProviderForTest(t *testing.T) {
	t.Helper()
	acpProviderMu.Lock()
	acpProviderCfg = ACPProviderConfig{}
	acpProviderMu.Unlock()
	t.Cleanup(func() {
		acpProviderMu.Lock()
		acpProviderCfg = ACPProviderConfig{}
		acpProviderMu.Unlock()
	})
}

// plantCatalogFile 落一份磁盘档并返回路径（模拟上一进程留下的缓存）。
func plantCatalogFile(t *testing.T) string {
	t.Helper()
	SetModelCatalogDir(t.TempDir())
	resetCatalogCache()
	t.Cleanup(func() { catalogDirFn = defaultCatalogDir; resetCatalogCache() })

	path := catalogFile(AgentQoder)
	writeCatalogFileForTest(t, path, catalogCacheFile{
		Agent: AgentQoder, AgentType: "qodercli",
		Options: []ModelOption{{Value: "qfmodel", Name: "Qwen3.8-Flash"}},
	})
	return path
}

// TestConfigureACPProviders_StartupKeepsDiskCache 启动（首次调用）不能删磁盘缓存。
func TestConfigureACPProviders_StartupKeepsDiskCache(t *testing.T) {
	path := plantCatalogFile(t)
	resetProviderForTest(t)

	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentQoder: {AgentType: "qodercli", SpawnCommand: []string{"qodercli", "--acp"}},
	}})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("启动就把磁盘缓存删了（%v）—— 重启后首个请求仍要等一次冷探测，"+
			"磁盘缓存等于没有。启动应只建立基线，不作废缓存", err)
	}
}

// TestConfigureACPProviders_RuntimeChangeDropsCache 运行期改了 spawn 才作废。
func TestConfigureACPProviders_RuntimeChangeDropsCache(t *testing.T) {
	path := plantCatalogFile(t)
	resetProviderForTest(t)

	// 第一次 = 启动基线
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentQoder: {AgentType: "qodercli", SpawnCommand: []string{"qodercli", "--acp"}},
	}})
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("启动阶段不该删缓存: %v", err)
	}

	// 第二次 = 运行期改了 spawn（settings.spawn_overrides 生效）
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{
		AgentQoder: {AgentType: "qodercli", SpawnCommand: []string{"D:/tools/qodercli.exe", "--acp"}},
	}})
	if _, err := os.Stat(path); err == nil {
		t.Fatal("spawn 命令变了却没作废清单缓存 —— 用户改完路径后界面会继续显示一整天的旧清单")
	}
}

// TestConfigureACPProviders_UnrelatedChangeKeepsCache 只改 Logger / 超时不该作废：
// 它们不影响"探测出来的是什么"，作废只会让用户白等 5~7s。
func TestConfigureACPProviders_UnrelatedChangeKeepsCache(t *testing.T) {
	path := plantCatalogFile(t)
	resetProviderForTest(t)

	cfg := config.ACPConfig{AgentType: "qodercli", SpawnCommand: []string{"qodercli", "--acp"}}
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{AgentQoder: cfg}})
	cfg.InitTimeout = 90e9 // 仅超时变化
	ConfigureACPProviders(ACPProviderConfig{Agents: map[string]config.ACPConfig{AgentQoder: cfg}})

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("只有超时变化却作废了缓存: %v", err)
	}
}
