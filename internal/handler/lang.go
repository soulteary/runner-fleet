package handler

import (
	"strings"

	"github.com/labstack/echo/v4"
	i18n "github.com/soulteary/i18n-kit/v4"
)

// langDetector 负责选界面语言，次序 ?lang= > cookie > X-Language > Accept-Language。
//
// query 排在 cookie 前面是刻意的：cookie 是这台浏览器上的长期偏好（语言下拉框写的就是它，
// 写完 reload，URL 上并不带参数），而 ?lang= 是本次访问的明确指定。次序反过来的话，
// 带 ?lang=ja 的链接发给一个早先选过中文的人，对方看到的仍是中文——这个参数对每一个
// 设过偏好的人都失效，而那恰恰是它唯一有用的场合。
//
// 它同样刻意不写回 cookie：一次性的链接参数不该悄悄改掉对方的长期偏好，去掉参数再刷新
// 就该回到自己选的那个语言。所以这里只用 i18n-kit 的 Detector，不用它的
// httpadapter.Middleware（那个会按 SetCookie 配置写 cookie，且把语言塞进 context，
// 我们两样都不需要）。v4 之前它是根包里的 StdMiddleware。
var langDetector = i18n.NewDetector(i18n.DetectorConfig{
	QueryParam: "lang",
	CookieName: "lang",
	Default:    i18n.LangEN,
})

// i18n-kit 自带 10 种语言（多出 it/es/pt/ru），而我们只有 6 份译文。
// Detector 判断一个取值可不可用走的是 Language.IsValid，读的就是这张表，
// 所以把它收窄到 supportedLangs，?lang=es 才会像 ?lang=xx 一样被跳过、继续看 cookie。
//
// 改的是 i18n-kit 的包级变量。这里是应用而不是库，进程内只有这一处用到 i18n-kit，
// 而且 kit 自己的 RegisterLanguage 也是这么改这张表的——这就是它给的配置口。
func init() {
	langs := make([]i18n.Language, 0, len(supportedLangs))
	for _, l := range supportedLangs {
		langs = append(langs, i18n.Language(l))
	}
	i18n.SupportedLanguages = langs
}

// echoLangSource 把 echo.Context 适配成 i18n.RequestSource（三个取值方法）。
//
// v3 把 Fiber 拆进 fiberadapter 时，根包仍然只认 net/http，Echo 得自己接这层；
// v4 连 net/http 也拆进了 httpadapter，根包如今一个框架都不认，只认 RequestSource
// 这个接口。同样的三个方法于是从「绕开 kit 的既定用法」变成了 kit 给第三种框架
// 留的正门：代码一行没动，理由变了。
type echoLangSource struct{ c echo.Context }

func (s echoLangSource) Query(name string) string { return s.c.QueryParam(name) }

func (s echoLangSource) Cookie(name string) string {
	v, err := s.c.Cookie(name)
	if err != nil || v == nil {
		return ""
	}
	return v.Value
}

func (s echoLangSource) Header(name string) string {
	if name == "Accept-Language" {
		return supportedAcceptLanguage(s.c.Request().Header.Get(name))
	}
	return s.c.Request().Header.Get(name)
}

// supportedAcceptLanguage 丢掉 Accept-Language 里我们没有译文的条目，其余条目连同 q 值原样保留。
//
// 必须在交给 Detector 之前过滤。它解析 Accept-Language 的那一步按 q 排序后返回第一个
// **ParseLanguage 认识**的语言，而 ParseLanguage 查的是 kit 内部那张别名表（10 种语言），
// 不是上面收窄过的 SupportedLanguages。于是 "es,ja" 会解析成 es，回到 Detect 里再被
// IsValid 挡掉，整条链就直接回落到 en —— 而正确答案是 ja。
//
// 过滤掉 es 之后，kit 的 q 排序照常生效，这正是换用它的理由：原先那段手写解析按
// 出现顺序取第一个能认的，完全无视 q，"en;q=0.1,de;q=0.9" 会选成 en。
func supportedAcceptLanguage(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.Split(header, ",")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		// 只取语言码用于判断，保留原条目（含 q）交给 kit
		code := strings.TrimSpace(part)
		if i := strings.Index(code, ";"); i >= 0 {
			code = strings.TrimSpace(code[:i])
		}
		if lang, ok := i18n.ParseLanguage(code); ok && lang.IsValid() {
			kept = append(kept, strings.TrimSpace(part))
		}
	}
	return strings.Join(kept, ",")
}

// resolveLang 选择界面语言，优先级为 ?lang= > cookie > X-Language > Accept-Language > en。
func resolveLang(c echo.Context) string {
	return langDetector.Detect(echoLangSource{c: c}).String()
}
