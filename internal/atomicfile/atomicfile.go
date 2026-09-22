// Package atomicfile 以「同目录临时文件 + fsync + rename」替换文件，
// 让并发读者只会看到旧内容或新内容之一，进程中途被杀也不会留下半截文件。
//
// 替代的是 os.WriteFile：它的顺序是 O_TRUNC、写入、关闭，于是这两件事都会发生——
// 读者在这中间读到空文件或半截内容（config.yaml 读空时 yaml.Unmarshal 并不报错，
// 界面上会短暂出现「一个 Runner 都没有」），以及进程在截断之后、写完之前退出时，
// 盘上永久留下一个坏掉的文件。rename 是同一文件系统内的原子操作，两者一并解决：
// 目标这个名字要么指向旧 inode，要么指向新 inode，没有中间态。
//
// 有一处行为与 os.WriteFile 不同，属主：rename 挂上的是新建的 inode，属主是 Manager 的 UID，
// 而就地写入保留原属主。按文档推荐的部署方式，config/ 与 runners/ 本来就 chown 给 1001，
// 所以没有影响；属主是其他 UID、而目录对 Manager 可写的少见情况下，写完属主会变成 Manager 的。
//
// 另一处是新建文件的权限：本包显式 chmod 到 perm，不经 umask，而 os.WriteFile 的 perm 要过 umask。
// 这是有意的——config.yaml 该是 0644 就该是 0644，不应随容器的 umask 变。
//
// internal/runner/agenttoken.go 的 EnsureAgentToken 不走这里：那里要的是「只允许第一个
// 写者」（os.Link 不覆盖已有的名字），这里要的是「最后一个写者覆盖」，语义相反。
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// rename 供测试注入。生产路径恒为 os.Rename。
var rename = os.Rename

// WriteFile 把 data 写入 path，语义对齐 os.WriteFile：
//   - 目标已存在时沿用其权限位（os.WriteFile 对已存在文件不改权限，这里保持一致）；
//   - 目标不存在时使用 perm。
//
// 两种环境下拿不到原子替换，此时回退为就地写入（即旧行为）并按路径告警一次，
// 而不是让一次本该成功的保存失败——升级不该把已经在跑的部署弄停：
//   - 目录不可写但文件可写，例如只 chown 了 config.yaml、没 chown config/，
//     此时同目录建不出临时文件；
//   - 目标是单文件 bind mount（-v ./config.yaml:/app/config/config.yaml），
//     此时 rename 报 EBUSY；临时文件与目标跨文件系统时报 EXDEV。
func WriteFile(path string, data []byte, perm os.FileMode) error {
	mode := perm
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	dir := filepath.Dir(path)
	// 临时文件必须与目标同目录：rename 只在同一文件系统内是原子的，
	// 放 /tmp 则跨文件系统时直接 EXDEV，等于这个包白做。
	// 名字以 . 开头，避免恰好被某些按目录列举配置的工具扫到。
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return writeInPlace(path, data, mode, err)
		}
		return fmt.Errorf("cannot create a temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()

	if err := writeTemp(f, data, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV) {
			return writeInPlace(path, data, mode, err)
		}
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}

	syncDir(dir)
	return nil
}

// writeTemp 写满临时文件并关闭。失败时已关闭 f，删除临时文件由调用方负责。
func writeTemp(f *os.File, data []byte, mode os.FileMode) error {
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot write the temporary file %s: %w", tmp, err)
	}
	// 先 fsync 再 rename。反过来的话，崩溃后可能出现「名字已经挂上、内容还在页缓存里」，
	// 那正是这个包要消灭的空文件，只是换了个地方产生。
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot flush the temporary file %s: %w", tmp, err)
	}
	// os.CreateTemp 建出来的是 0600，改成目标应有的权限。用 f.Chmod 而不是 os.Chmod(tmp)：
	// 改的是这个 fd 指向的 inode，不依赖临时文件那个名字仍然有效。
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot set the mode of the temporary file %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot write the temporary file %s: %w", tmp, err)
	}
	return nil
}

// writeInPlace 回退为 os.WriteFile 的旧行为，并按路径告警一次。
func writeInPlace(path string, data []byte, mode os.FileMode, cause error) error {
	warnOnce(path, cause)
	return os.WriteFile(path, data, mode)
}

// warnedPaths 同一个路径只告警一次：config.yaml 的每次保存都会走到这里，
// 每次都打就成了刷屏；完全不打则是把「这台机器上保存不是崩溃安全的」咽了下去。
var warnedPaths sync.Map // path -> *sync.Once

func warnOnce(path string, cause error) {
	v, _ := warnedPaths.LoadOrStore(path, &sync.Once{})
	v.(*sync.Once).Do(func() {
		log.Printf("warning: cannot replace %s atomically (%v); writing it in place instead. "+
			"Make its directory writable by the Manager's user to get crash-safe saves", path, cause)
	})
}

// syncDir 尽力把目录项刷到盘上。rename 之后不 fsync 目录，崩溃后可能出现
// 「新 inode 的内容已落盘、目录里指向它的那个名字还没落盘」——文件会回到旧内容，
// 不会损坏，所以这一步失败只是少一层保障，不值得让一次已经成功的替换返回错误。
// 只读打开目录再 fsync 也不是所有文件系统都支持，同样忽略。
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync()
}
