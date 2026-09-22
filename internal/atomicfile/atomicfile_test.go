package atomicfile

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// swapRename 换掉包级的 rename 并在测试结束时换回。
//
// 本包的测试一律不 t.Parallel：rename 是包级变量，并行跑就是 -race 下的数据竞争，
// 而这个包存在的理由正是并发安全，测试自己踩一个竞态没有道理。
func swapRename(t *testing.T, fn func(oldpath, newpath string) error) {
	t.Helper()
	orig := rename
	rename = fn
	t.Cleanup(func() { rename = orig })
}

// captureLog 接管 log 的输出并返回取内容的函数。
func captureLog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return buf.String
}

// tempLeftovers 返回目录里残留的临时文件。
func tempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, ".*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWriteFile_NewFileUsesPerm 目标不存在时按 perm 建。
//
// 断言的是精确相等而不是「不超过 perm」：实现显式 chmod 到 perm，不过 umask，
// 所以这里是确定的。这一点与 os.WriteFile 不同，是有意的——config.yaml 该是 0644
// 就该是 0644，不随容器的 umask 变——因此值得被钉住。
func TestWriteFile_NewFileUsesPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "hello" {
		t.Errorf("内容 %q，期望 %q", got, "hello")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0644 {
		t.Errorf("权限 %v，期望 %v", fi.Mode().Perm(), os.FileMode(0644))
	}
}

// TestWriteFile_PreservesExistingMode 目标已存在时沿用它的权限位，
// 与 os.WriteFile 一致——不能因为换了写法就把用户 chmod 过的文件改回 0644。
func TestWriteFile_PreservesExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil { // 绕开 umask，确保起点确实是 0600
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "new" {
		t.Errorf("内容 %q，期望 %q", got, "new")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("权限 %v，期望沿用 %v", fi.Mode().Perm(), os.FileMode(0600))
	}
}

// TestWriteFile_NoTempLeftovers 成功与失败两条路径都不留临时文件。
//
// 留下的话，config/ 会随每次保存失败攒出一堆 .config.yaml.tmp-*，
// 而这个目录是文档里让人备份的那个。
func TestWriteFile_NoTempLeftovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := WriteFile(path, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("成功写入后仍有临时文件：%v", leftovers)
	}

	// 非 EBUSY/EXDEV 的 rename 失败不走回退，错误要原样交给调用方。
	injected := &os.LinkError{Op: "rename", Err: syscall.EIO}
	swapRename(t, func(string, string) error { return injected })
	err := WriteFile(path, []byte("boom"), 0644)
	if err == nil {
		t.Fatal("rename 失败时应返回错误")
	}
	if !errors.Is(err, syscall.EIO) {
		t.Errorf("错误未透出底层原因：%v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("错误未指出是哪个文件：%v", err)
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Errorf("写入失败后仍有临时文件：%v", leftovers)
	}
	if got := mustRead(t, path); got != "ok" {
		t.Errorf("rename 失败后目标应保持旧内容，got %q", got)
	}
}

// TestWriteFile_TargetUntouchedUntilRename 确定性地证明原子性：
// 数据全部写完、rename 尚未执行的那一刻，目标读出来仍是旧内容。
//
// 变异验证：把 WriteFile 的实现换回 os.WriteFile，这条测试变红——
// 那条路径根本不会调用注入的 rename，called 保持 false。
func TestWriteFile_TargetUntouchedUntilRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	called := false
	var seen string
	swapRename(t, func(oldpath, newpath string) error {
		called = true
		// 临时文件必须与目标同目录，否则跨文件系统时 rename 根本不是原子的（EXDEV），
		// 这个包也就白做了——放 /tmp 是最容易犯的那个错。
		if filepath.Dir(oldpath) != filepath.Dir(newpath) {
			t.Errorf("临时文件在 %s，目标在 %s，不同目录", filepath.Dir(oldpath), filepath.Dir(newpath))
		}
		b, err := os.ReadFile(newpath)
		if err != nil {
			t.Errorf("rename 之前目标应当仍然存在：%v", err)
		}
		seen = string(b)
		return os.Rename(oldpath, newpath)
	})

	if err := WriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("没有经过 rename，替换不是原子的")
	}
	if seen != "old" {
		t.Errorf("rename 之前目标已被改动，读到 %q，期望 %q", seen, "old")
	}
	if got := mustRead(t, path); got != "new" {
		t.Errorf("rename 之后内容 %q，期望 %q", got, "new")
	}
}

// TestWriteFile_FallsBackOnRenameRefused 单文件 bind mount（EBUSY）与跨文件系统（EXDEV）
// 下 rename 用不了，此时回退为就地写入，且同一路径只告警一次。
//
// 告警只打一次这件事本身要被钉住：config.yaml 每次保存都会走到这里，
// 按次打印会把 Runner 状态轮询的日志淹掉。
func TestWriteFile_FallsBackOnRenameRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"EBUSY", syscall.EBUSY}, // 单文件 bind mount
		{"EXDEV", syscall.EXDEV}, // 临时文件与目标不在同一文件系统
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			swapRename(t, func(string, string) error {
				return &os.LinkError{Op: "rename", Err: tc.err}
			})
			logged := captureLog(t)

			// 写两次：告警应当只出现一次。
			for i, want := range []string{"first", "second"} {
				if err := WriteFile(path, []byte(want), 0644); err != nil {
					t.Fatalf("第 %d 次写入应回退为就地写入并成功：%v", i+1, err)
				}
				if got := mustRead(t, path); got != want {
					t.Fatalf("第 %d 次写入后内容 %q，期望 %q", i+1, got, want)
				}
			}

			if n := strings.Count(logged(), "writing it in place instead"); n != 1 {
				t.Errorf("告警打印了 %d 次，期望 1 次：\n%s", n, logged())
			}
			if !strings.Contains(logged(), path) {
				t.Errorf("告警未指出是哪个文件：\n%s", logged())
			}
			if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
				t.Errorf("回退后仍有临时文件：%v", leftovers)
			}
		})
	}
}

// TestWriteFile_FallsBackWhenDirNotWritable 只 chown 了 config.yaml、没 chown config/ 的部署：
// 同目录建不出临时文件，回退为就地写入，保存仍然成功。
//
// 这条是升级兼容性的护栏——这种部署此前是能正常保存的，不能因为换了写法就开始报错。
func TestWriteFile_FallsBackWhenDirNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受权限位约束，建不出「目录不可写」的场景")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	// t.TempDir 的清理要能删掉目录里的东西，权限得还回去
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	logged := captureLog(t)
	if err := WriteFile(path, []byte("new"), 0644); err != nil {
		t.Fatalf("目录不可写时应回退为就地写入：%v", err)
	}
	if got := mustRead(t, path); got != "new" {
		t.Errorf("内容 %q，期望 %q", got, "new")
	}
	if n := strings.Count(logged(), "writing it in place instead"); n != 1 {
		t.Errorf("告警打印了 %d 次，期望 1 次：\n%s", n, logged())
	}
}
