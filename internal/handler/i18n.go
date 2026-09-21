package handler

import (
	"fmt"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
)

// 服务端消息的本地化。
//
// 界面外壳早就有六种语言，但用户操作之后真正读到的那一句——toast 里的提示、
// 失败时的错误——一直是 handler 里的中文字面量。把界面切到 English，外壳是英文，
// 每一个 toast 是中文。文档承诺了六种语言的使用体验，产品此前只兑现了外壳。
//
// 做法上没有新东西：resolveLang 与 I18nLoader 都已经存在（前者给模板选语言，
// 后者由 main 从 embed 注入），这里只是把它们接到 API 响应这一侧。
//
// 选择在**服务端**翻译而不是返回错误码由浏览器翻译，有两个理由：
//   - API 的 message 字段仍然是给人读的散文。返回 "api.name_required" 会让
//     curl 与 CI 脚本拿到一个码，而它们恰恰是最没有翻译表可查的调用方。
//   - Detector 的默认语言是 en，所以不带 Accept-Language 的脚本会拿到英文——
//     比今天拿到中文严格更好。
//
// 键一律以 api. 开头，与界面自己的 msg.* 分开：那些由 app.js 查表，
// 这些由这里查表，两套的生命周期不一样，混在一个前缀下迟早会有人删错。

// bundleCache 缓存「语言 -> 翻译表」。
//
// I18nLoader 每次调用都要读一遍内嵌文件并 json.Unmarshal，而 API 这一侧是每个
// 请求都要查表的——列表页 15 秒轮询一次，每次都重新解析一份 JSON 属于白烧。
// 翻译表在进程生命周期内不变（内容编译进二进制），所以缓存不设过期。
var bundleCache sync.Map // string -> map[string]string

func bundle(lang string) map[string]string {
	if v, ok := bundleCache.Load(lang); ok {
		m, _ := v.(map[string]string)
		return m
	}
	var m map[string]string
	if I18nLoader != nil {
		m, _ = I18nLoader(lang)
	}
	if m == nil {
		m = map[string]string{}
	}
	bundleCache.Store(lang, m)
	return m
}

// ResetI18nCache 丢掉已缓存的翻译表，供测试在替换 I18nLoader 之后调用。
//
// 导出是因为 cmd/runner-manager 的用例也会换掉 I18nLoader：不清缓存的话，
// 先跑的那个用例装进去的表会被后面的用例读到，而那种串扰只在改了测试顺序时才暴露。
func ResetI18nCache() { bundleCache = sync.Map{} }

// tr 取当前请求语言下 key 对应的文案。
//
// 三级回落：请求语言 → 英文 → 键本身。最后一级刻意不回落成中文：键漏了的时候，
// 界面上出现 "api.start_failed" 是一眼能看出的缺陷，而出现一句中文只会被当成
// 「这条还没翻译」，在一个号称六语言的界面里混过去。
func tr(c echo.Context, key string) string {
	if s, ok := lookup(c, key); ok {
		return s
	}
	return key
}

// trf 同 tr，再按 fmt.Sprintf 填参数。
//
// 键缺失时**不做** Sprintf。回落值是键本身，里面没有任何动词，Sprintf 会把参数
// 追加成 "api.start_failed%!(EXTRA string=new)" 这种噪音——那串东西既不像译文缺失，
// 也不像参数，读到的人只会以为程序坏了。改成 "键: 参数" 至少把两件事都留着，
// 而且一眼看得出是缺了一条译文。
func trf(c echo.Context, key string, args ...any) string {
	if s, ok := lookup(c, key); ok {
		return fmt.Sprintf(s, args...)
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, key)
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a))
	}
	return strings.Join(parts, ": ")
}

// lookup 三级回落：请求语言 → 英文 → 没有。
func lookup(c echo.Context, key string) (string, bool) {
	lang := resolveLang(c)
	if s, ok := bundle(lang)[key]; ok && s != "" {
		return s, true
	}
	if lang != "en" {
		if s, ok := bundle("en")[key]; ok && s != "" {
			return s, true
		}
	}
	return "", false
}
