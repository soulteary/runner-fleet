package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func ptr(b bool) *bool { return &b }

// 三态要能原样存回来：查不出答案必须区别于「查出来是没有」
func TestGitHubStatus_RoundTripsThreeStates(t *testing.T) {
	cases := []struct {
		name      string
		write     *bool
		writeBusy *bool
		writeErr  string
	}{
		{name: "已登记且空闲", write: ptr(true), writeBusy: ptr(false)},
		{name: "已登记且忙碌", write: ptr(true), writeBusy: ptr(true)},
		{name: "确实没登记", write: ptr(false), writeBusy: nil},
		{name: "查不出来", write: nil, writeBusy: nil, writeErr: "GitHub 返回 401：令牌无效或已过期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const at0 = "2026-09-20T09:00:00Z"
			if err := WriteGitHubStatus(dir, GitHubStatus{
				Registered: tc.write, Busy: tc.writeBusy, Error: tc.writeErr, LastCheck: at0,
			}); err != nil {
				t.Fatal(err)
			}
			st := ReadGitHubStatus(dir)
			got, gotBusy, at, gotErr := st.Registered, st.Busy, st.LastCheck, st.Error
			if at != at0 {
				t.Fatalf("检查时间应原样存回，写入 %q 读回 %q", at0, at)
			}
			switch {
			case tc.writeBusy == nil && gotBusy != nil:
				t.Fatalf("忙碌状态写入未知，读回 %v", *gotBusy)
			case tc.writeBusy != nil && gotBusy == nil:
				t.Fatalf("忙碌状态写入 %v，读回未知", *tc.writeBusy)
			case tc.writeBusy != nil && *gotBusy != *tc.writeBusy:
				t.Fatalf("忙碌状态写入 %v，读回 %v", *tc.writeBusy, *gotBusy)
			}
			switch {
			case tc.write == nil && got != nil:
				t.Fatalf("写入未知，读回 %v", *got)
			case tc.write != nil && got == nil:
				t.Fatalf("写入 %v，读回未知", *tc.write)
			case tc.write != nil && *got != *tc.write:
				t.Fatalf("写入 %v，读回 %v", *tc.write, *got)
			}
			if gotErr != tc.writeErr {
				t.Fatalf("原因说明写入 %q 读回 %q", tc.writeErr, gotErr)
			}
		})
	}
}

// 老版本写下的文件里 registered 是普通 bool、没有 error 字段，语义不能变
func TestGitHubStatus_ReadsLegacyFormat(t *testing.T) {
	for _, legacy := range []struct {
		body string
		want bool
	}{
		{`{"registered":true,"last_check":"2026-01-01T00:00:00Z"}`, true},
		{`{"registered":false,"last_check":"2026-01-01T00:00:00Z"}`, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, GitHubStatusFile), []byte(legacy.body), 0644); err != nil {
			t.Fatal(err)
		}
		st := ReadGitHubStatus(dir)
		got, gotBusy, at, gotErr := st.Registered, st.Busy, st.LastCheck, st.Error
		if got == nil || *got != legacy.want {
			t.Fatalf("老格式 %s 应读出 %v，得到 %v", legacy.body, legacy.want, got)
		}
		// 老文件里没有 busy 字段：那是「不知道」，不能当成「不忙」
		if gotBusy != nil {
			t.Fatalf("老格式没有 busy 字段，应读成未知，得到 %v", *gotBusy)
		}
		if at != "2026-01-01T00:00:00Z" || gotErr != "" {
			t.Fatalf("老格式读出 at=%q err=%q", at, gotErr)
		}
		// 老文件同样没有 public / visibility_checked_at：那是「还没查过可见性」，
		// 读成 false 会给每一个升级上来的部署挂上「私有」的结论
		if st.Public != nil || st.VisibilityCheckedAt != "" {
			t.Fatalf("老格式没有可见性字段，应读成未查过，得到 %v/%q", st.Public, st.VisibilityCheckedAt)
		}
	}
}

// 可见性是第三个三态字段，和上面两个同样不能把「不知道」存成「不是」
func TestGitHubStatus_RoundTripsVisibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		public *bool
	}{
		{"公开", ptr(true)},
		{"私有", ptr(false)},
		{"不知道", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			const at = "2026-09-20T09:00:00Z"
			if err := WriteGitHubStatus(dir, GitHubStatus{Public: tc.public, VisibilityCheckedAt: at}); err != nil {
				t.Fatal(err)
			}
			st := ReadGitHubStatus(dir)
			switch {
			case tc.public == nil && st.Public != nil:
				t.Fatalf("写入未知，读回 %v", *st.Public)
			case tc.public != nil && st.Public == nil:
				t.Fatalf("写入 %v，读回未知", *tc.public)
			case tc.public != nil && *st.Public != *tc.public:
				t.Fatalf("写入 %v，读回 %v", *tc.public, *st.Public)
			}
			if st.VisibilityCheckedAt != at {
				t.Fatalf("可见性查询时间写入 %q 读回 %q", at, st.VisibilityCheckedAt)
			}
		})
	}
}

// 没有文件 = 从未检查，和「查过但失败」不同：后者有检查时间
func TestGitHubStatus_MissingFileIsNeverChecked(t *testing.T) {
	st := ReadGitHubStatus(t.TempDir())
	if st != (GitHubStatus{}) {
		t.Fatalf("没有状态文件时应一概为空，得到 %+v", st)
	}
}

// 模板辅助方法：{{if .RegisteredOnGitHub}} 对指针只看是否为 nil，
// 指向 false 的指针同样为真，所以模板必须走这三个方法
func TestRunnerInfo_GitHubTriStateHelpers(t *testing.T) {
	cases := []struct {
		name             string
		v                *bool
		yes, no, unknown bool
	}{
		{"已登记", ptr(true), true, false, false},
		{"没登记", ptr(false), false, true, false},
		{"未知", nil, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := RunnerInfo{RegisteredOnGitHub: tc.v}
			if info.GitHubYes() != tc.yes || info.GitHubNo() != tc.no || info.GitHubUnknown() != tc.unknown {
				t.Fatalf("yes/no/unknown = %v/%v/%v，期望 %v/%v/%v",
					info.GitHubYes(), info.GitHubNo(), info.GitHubUnknown(), tc.yes, tc.no, tc.unknown)
			}
		})
	}
}

// GitHubPublic 同样是 *bool。{{if .GitHubPublic}} 会让每一个私有仓库都挂上
// 「公开仓库」徽标——这个字段存在的意义正是把公开仓库挑出来，误报等于没做。
func TestRunnerInfo_GitHubPublicHelper(t *testing.T) {
	cases := []struct {
		name string
		v    *bool
		want bool
	}{
		{"公开", ptr(true), true},
		{"私有", ptr(false), false},
		{"未知", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (RunnerInfo{GitHubPublic: tc.v}).GitHubPublicYes(); got != tc.want {
				t.Fatalf("GitHubPublicYes() = %v，期望 %v", got, tc.want)
			}
		})
	}
}

// GitHubBusy 也是 *bool，模板里写 {{if .GitHubBusy}} 会把「空闲」显示成「忙碌中」
func TestRunnerInfo_GitHubBusyHelper(t *testing.T) {
	cases := []struct {
		name string
		v    *bool
		want bool
	}{
		{"忙碌", ptr(true), true},
		{"空闲", ptr(false), false},
		{"未知", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (RunnerInfo{GitHubBusy: tc.v}).GitHubBusyYes(); got != tc.want {
				t.Fatalf("GitHubBusyYes() = %v，期望 %v", got, tc.want)
			}
		})
	}
}
