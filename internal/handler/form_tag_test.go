package handler

import (
	"reflect"
	"testing"
)

// 写接口的请求体只接受 JSON。
//
// 跨站 form 提交是 CORS 意义上的「简单请求」——不触发预检，浏览器照发，并自动带上
// 该源已缓存的 Basic Auth 凭据，是 CSRF 最好用的载体。csrfGuardMiddleware 是主防线，
// 「结构体上没有 form tag」是第二道：即便中间件被绕过或配错，form 编码的请求也只能
// 绑出一个空结构体，随即被必填校验挡掉。
//
// 界面本来就用 JSON 提交（FormData 只用于就地读取表单字段），所以这不影响现有前端。
func TestWriteRequestStructsRejectFormBinding(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"AddRunnerRequest", reflect.TypeOf(AddRunnerRequest{})},
		{"UpdateRunnerRequest", reflect.TypeOf(UpdateRunnerRequest{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < tc.typ.NumField(); i++ {
				f := tc.typ.Field(i)
				if tag, ok := f.Tag.Lookup("form"); ok {
					t.Errorf("%s.%s 带着 form tag（%q）：写接口只接受 JSON，"+
						"form 编码会重新打开跨站提交的口子", tc.name, f.Name, tag)
				}
				// json tag 反过来是必须的，否则字段名大小写变化会悄悄改掉 API
				if _, ok := f.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s 缺少 json tag", tc.name, f.Name)
				}
			}
		})
	}
}
