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
		name     string
		write    *bool
		writeErr string
	}{
		{name: "已登记", write: ptr(true)},
		{name: "确实没登记", write: ptr(false)},
		{name: "查不出来", write: nil, writeErr: "GitHub 返回 401：令牌无效或已过期"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := WriteGitHubStatus(dir, tc.write, tc.writeErr); err != nil {
				t.Fatal(err)
			}
			got, at, gotErr := readGitHubStatus(dir)
			if at == "" {
				t.Fatal("应记录检查时间")
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
		got, at, gotErr := readGitHubStatus(dir)
		if got == nil || *got != legacy.want {
			t.Fatalf("老格式 %s 应读出 %v，得到 %v", legacy.body, legacy.want, got)
		}
		if at != "2026-01-01T00:00:00Z" || gotErr != "" {
			t.Fatalf("老格式读出 at=%q err=%q", at, gotErr)
		}
	}
}

// 没有文件 = 从未检查，和「查过但失败」不同：后者有检查时间
func TestGitHubStatus_MissingFileIsNeverChecked(t *testing.T) {
	got, at, gotErr := readGitHubStatus(t.TempDir())
	if got != nil || at != "" || gotErr != "" {
		t.Fatalf("没有状态文件时应一概为空，得到 %v/%q/%q", got, at, gotErr)
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
