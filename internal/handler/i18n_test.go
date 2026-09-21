package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// withI18n 临时装一份翻译表，返回还原函数。
//
// 不走真实的 JSON：那些文件嵌在 cmd/runner-manager 里，本包够不着；而且用例要断言的
// 是「这条响应用了哪个键」，不是「那个键当前译成了什么字」——后者改一次文案就红一次。
func withI18n(m map[string]string) func() {
	prev := I18nLoader
	I18nLoader = func(string) (map[string]string, error) { return m, nil }
	ResetI18nCache()
	return func() {
		I18nLoader = prev
		ResetI18nCache()
	}
}

func trCtx(t *testing.T, lang string) echo.Context {
	t.Helper()
	req := httptest.NewRequest("GET", "/?lang="+lang, nil)
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// 键缺失时 trf 不该把参数 Sprintf 进一个没有动词的字符串。
//
// 第一版就是这么写的，结果是 "api.recreate_requires_registered%!(EXTRA string=new)"——
// 既不像缺译文也不像参数，读到的人只会以为程序坏了。现在退成「键: 参数」。
func TestTrf_MissingKeyDoesNotProduceSprintfNoise(t *testing.T) {
	defer withI18n(map[string]string{})()
	got := trf(trCtx(t, "en"), "api.nope", "new")
	if got != "api.nope: new" {
		t.Fatalf("缺键时应退成「键: 参数」，得到 %q", got)
	}
	if want := "%!"; len(got) > 0 && contains(got, want) {
		t.Fatalf("缺键时不该出现 Sprintf 的报错标记: %q", got)
	}
}

// 请求语言优先，其次英文，最后才是键本身。
func TestTr_FallsBackThroughEnglishThenKey(t *testing.T) {
	defer withI18n(map[string]string{"api.only_en": "English text"})()
	if got := tr(trCtx(t, "ja"), "api.only_en"); got != "English text" {
		t.Fatalf("该语言缺键时应回落英文，得到 %q", got)
	}
	if got := tr(trCtx(t, "ja"), "api.absent"); got != "api.absent" {
		t.Fatalf("两边都缺时应返回键本身，得到 %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
