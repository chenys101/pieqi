package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

// gunzipBody 解压响应体（非 gzip 时原样返回）。
func gunzipBody(t *testing.T, r *httptest.ResponseRecorder) []byte {
	t.Helper()
	if r.Header().Get("Content-Encoding") != "gzip" {
		return r.Body.Bytes()
	}
	zr, err := gzip.NewReader(bytes.NewReader(r.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip 头无法解析: %v", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("解压失败: %v", err)
	}
	return out
}

// TestGzip_ListTasksCompressed 大响应必须压缩，且**解压后内容与不压缩时一致**。
//
// 只断言 Content-Encoding: gzip 是不够的 —— 头对了但内容坏了（漏 Flush、没删
// Content-Length 导致截断）同样是故障，而且更隐蔽。
func TestGzip_ListTasksCompressed(t *testing.T) {
	_, store, r := setupAPITest(t)
	// 造一个够大的任务（超过 gzipMinBytes），否则会被小响应阈值挡掉。
	big := strings.Repeat("x", 4096)
	for i := 0; i < 5; i++ {
		if _, err := store.Create(&model.Task{
			ProjectID:   "g",
			ProjectPath: "/repo",
			Prompt:      "p",
			Output:      big,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 基线：不带 Accept-Encoding ⇒ 不压缩，拿它做内容对照。
	plain := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/tasks", nil)
	r.ServeHTTP(plain, req)
	if enc := plain.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("未声明 Accept-Encoding 时不应压缩，got %q", enc)
	}

	w := httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding=%q，want gzip", got)
	}
	// Vary 保证中间缓存不会把压缩版发给不支持的客户端。
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Fatalf("Vary=%q，应含 Accept-Encoding", v)
	}
	// Content-Length 必须去掉：留着它是**未压缩**的长度，浏览器会按它截断。
	if cl := w.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("压缩响应不应带 Content-Length（那是未压缩长度），got %q", cl)
	}
	// 体积必须真的变小。
	if w.Body.Len() >= plain.Body.Len() {
		t.Fatalf("压缩后 %d 字节不小于未压缩 %d 字节", w.Body.Len(), plain.Body.Len())
	}
	// 内容必须能被完整解出来，且与未压缩版语义相等。
	var got, want any
	if err := json.Unmarshal(gunzipBody(t, w), &got); err != nil {
		t.Fatalf("解压后不是合法 JSON: %v", err)
	}
	if err := json.Unmarshal(plain.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if !bytes.Equal(gj, wj) {
		t.Fatalf("压缩与未压缩的内容不一致")
	}
}

// TestGzip_SkipsExcludedPaths 排除路径**绝不能被 gzip 包装**。
//
// 这几条漏编译了会静默出坏数据：/api/ws 是 101 握手、preview 是代理 + HTML 重写、
// file 是二进制透传（压缩会破坏 Range/ETag）。
func TestGzip_SkipsExcludedPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GzipMiddleware())
	// 用回声路由模拟：只要能拿到请求，就说明中间件没有破坏这条链路。
	paths := []string{
		"/api/ws",
		"/api/tasks/abc/preview/",
		"/api/tasks/abc/preview/index.html",
		"/api/tasks/abc/file",
		"/preview/abc/index.html",
	}
	for _, p := range paths {
		r.GET(p, func(c *gin.Context) {
			c.String(http.StatusOK, strings.Repeat("payload-", 1000))
		})
	}
	for _, p := range paths {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", p, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		r.ServeHTTP(w, req)
		if enc := w.Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("%s 不应被压缩，got Content-Encoding=%q", p, enc)
		}
	}
}

// TestGzip_RespectsQZero `gzip;q=0` 是客户端**显式拒绝**（RFC 9110），必须不压。
func TestGzip_RespectsQZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GzipMiddleware())
	r.GET("/big", func(c *gin.Context) { c.String(http.StatusOK, strings.Repeat("z", 2000)) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/big", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0")
	r.ServeHTTP(w, req)
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("gzip;q=0 表示拒绝，不应压缩，got %q", enc)
	}
}

// TestGzip_ErrorResponsesPassthrough 非 2xx 不压缩（错误体很小，保持可读便于排查）。
func TestGzip_ErrorResponsesPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GzipMiddleware())
	r.GET("/missing", func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": strings.Repeat("e", 2000)})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/missing", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r.ServeHTTP(w, req)
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("404 不应压缩，got %q", enc)
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d", w.Code)
	}
}
