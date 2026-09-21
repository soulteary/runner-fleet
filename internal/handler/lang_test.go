package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/labstack/echo/v4"
	i18n "github.com/soulteary/i18n-kit/v3"
)

func langCtx(t *testing.T, query, cookie, acceptLang, xLang string) echo.Context {
	t.Helper()
	target := "/"
	if query != "" {
		target += "?lang=" + url.QueryEscape(query)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "lang", Value: cookie})
	}
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	if xLang != "" {
		req.Header.Set("X-Language", xLang)
	}
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// i18n-kit 认识 10 种语言，我们只有 6 份译文。init() 把 kit 的 SupportedLanguages
// 收窄到 supportedLangs，这条用例守的就是那次收窄——没有它，?lang=es 会被当成合法取值，
// 界面语言变成一个根本没有译文的 es。
func TestSupportedLanguagesIsNarrowedToOurTranslations(t *testing.T) {
	if len(i18n.SupportedLanguages) != len(supportedLangs) {
		t.Fatalf("i18n.SupportedLanguages = %v，期望与 supportedLangs %v 等长",
			i18n.SupportedLanguages, supportedLangs)
	}
	for _, want := range supportedLangs {
		if !i18n.Language(want).IsValid() {
			t.Errorf("%q 有译文却不被 IsValid 认可", want)
		}
	}
	// 反向：kit 自带但我们没有译文的语言必须被挡掉
	for _, unsupported := range []string{"it", "es", "pt", "ru"} {
		if i18n.Language(unsupported).IsValid() {
			t.Errorf("%q 没有译文，不该通过 IsValid", unsupported)
		}
	}
}

// 这条是换用 i18n-kit 的理由：原先那段手写解析按出现顺序取第一个能认的语言，完全无视 q 值。
// 实测 origin/main 的实现对下面第一行返回 "en"——而客户端明说了更想要 de（q=0.9 > q=0.1）。
func TestResolveLang_AcceptLanguageHonorsQuality(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"en;q=0.1,de;q=0.9", "de"}, // 旧实现给 "en"，是 bug
		{"de;q=0.9,en;q=0.1", "de"},
		{"ja;q=0.2,ko;q=0.8", "ko"},
		{"de-DE,de;q=0.9,en;q=0.8", "de"}, // 无 q 者视为 1.0，仍是 de
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			if got := resolveLang(langCtx(t, "", "", tc.header, "")); got != tc.want {
				t.Fatalf("resolveLang = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// supportedAcceptLanguage 的存在理由，也是最容易在重构里丢掉的一条。
//
// 不过滤的话：kit 的 Accept-Language 解析按 q 排序后返回第一个 ParseLanguage 认识的语言，
// 而 ParseLanguage 查的是 kit 内部 10 种语言的别名表、不是收窄后的 SupportedLanguages。
// 于是 "es,ja" 解析成 es，回到 Detect 里被 IsValid 挡掉，整条链直接回落 en。
// 实测 origin/main 对 "es,ja" 返回 "ja"——所以那会是一次实打实的退化。
func TestResolveLang_UnsupportedAcceptLanguageFallsThroughToNext(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"首选无译文，取下一个有译文的", "es,ja", "ja"},
		{"多个无译文的都跳过", "es,it,pt,ko", "ko"},
		{"无译文者 q 更高也要跳过", "es;q=0.9,ja;q=0.1", "ja"},
		{"全都没有译文时回落英文", "es,it,pt,ru", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveLang(langCtx(t, "", "", tc.header, "")); got != tc.want {
				t.Fatalf("resolveLang(%q) = %q，期望 %q", tc.header, got, tc.want)
			}
		})
	}
}

// 换 kit 之后新拿到的能力：query / cookie 上的地区变体也能归一化。
// 旧实现只对 Accept-Language 里的值切 "-"，?lang=zh-CN 是直接不认的（实测返回空、继续往下找）。
func TestResolveLang_NormalizesRegionalVariants(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		cookie       string
		want         string
		wasBrokenOld bool
	}{
		{name: "query 带地区", query: "zh-CN", want: "zh", wasBrokenOld: true},
		{name: "query 下划线写法", query: "zh_TW", want: "zh", wasBrokenOld: true},
		{name: "cookie 带地区", cookie: "ja-JP", want: "ja", wasBrokenOld: true},
		{name: "大小写不敏感", query: "ZH", want: "zh"},
		{name: "前后空白", query: "  ja  ", want: "ja"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveLang(langCtx(t, tc.query, tc.cookie, "", "")); got != tc.want {
				t.Fatalf("resolveLang = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// 没有译文的取值必须像非法取值一样被跳过，而不是被当成合法语言用上。
func TestResolveLang_UnsupportedValuesSkippedInEveryStep(t *testing.T) {
	if got := resolveLang(langCtx(t, "es", "ja", "", "")); got != "ja" {
		t.Fatalf("?lang=es 应跳过并继续看 cookie，得到 %q", got)
	}
	if got := resolveLang(langCtx(t, "", "es", "ko", "")); got != "ko" {
		t.Fatalf("cookie=es 应跳过并继续看 Accept-Language，得到 %q", got)
	}
	if got := resolveLang(langCtx(t, "es", "it", "pt", "")); got != "en" {
		t.Fatalf("三处都没有译文时应回落 en，得到 %q", got)
	}
}

// X-Language 排在 cookie 之后、Accept-Language 之前，是换 kit 之后顺带多出来的一档，
// 这里把次序钉住，免得日后改 Priority 时无声地挪位。
func TestResolveLang_XLanguageSitsBetweenCookieAndAcceptLanguage(t *testing.T) {
	if got := resolveLang(langCtx(t, "", "ja", "", "ko")); got != "ja" {
		t.Fatalf("cookie 应优先于 X-Language，得到 %q", got)
	}
	if got := resolveLang(langCtx(t, "", "", "de", "ko")); got != "ko" {
		t.Fatalf("X-Language 应优先于 Accept-Language，得到 %q", got)
	}
}

// resolveLang 只读不写：一次性的 ?lang= 不该改掉浏览器上的长期偏好。
// i18n-kit 的 StdMiddleware 是会按配置写 cookie 的，这条用例守住「我们没用它」。
func TestResolveLang_DoesNotWriteCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/?lang=ja", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "zh"})
	c := echo.New().NewContext(req, rec)

	if got := resolveLang(c); got != "ja" {
		t.Fatalf("resolveLang = %q，期望 ja", got)
	}
	if sc := rec.Result().Header.Values("Set-Cookie"); len(sc) != 0 {
		t.Fatalf("resolveLang 不该写 cookie，却发出了 %v", sc)
	}
}

func TestSupportedAcceptLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"ja", "ja"},
		{"es", ""},
		{"es,ja", "ja"},
		{"ja,es", "ja"},
		{"es;q=0.9,ja;q=0.1", "ja;q=0.1"}, // q 值原样保留，交给 kit 排序
		{"de-DE,de;q=0.9,en;q=0.8", "de-DE,de;q=0.9,en;q=0.8"},
		{"es,it,pt,ru", ""},
		{"  ja  ,  es  ", "ja"}, // 条目两侧空白去掉
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := supportedAcceptLanguage(tc.in); got != tc.want {
				t.Fatalf("supportedAcceptLanguage(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}
