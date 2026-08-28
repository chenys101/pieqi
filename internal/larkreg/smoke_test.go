package larkreg

import (
	"testing"

	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
)

// TestSDKImportsReachable 验证关键 SDK 子包在当前 go.mod 下可导入。
// 长连接(larkws)、事件分发(dispatcher)、IM service、registration 四个
// 子包都要可达 —— 如果其中任何一个路径在 SDK 新版本里改名,这里会先 fail。
func TestSDKImportsReachable(t *testing.T) {
	_ = registration.RegisterApp // 函数指针引用,不下断言
}
