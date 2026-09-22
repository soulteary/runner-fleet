package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soulteary/runner-fleet/internal/config"
)

// legacyPath / storePath 两个位置各自的路径，测试里反复用到
func legacyPath(installDir string) string { return filepath.Join(installDir, LegacyRunnerTokenFile) }

func writeLegacy(t *testing.T, installDir, token string) {
	t.Helper()
	if err := os.MkdirAll(installDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath(installDir), []byte(token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeStore(t *testing.T, s *Store, name, token string) {
	t.Helper()
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		t.Fatal(err)
	}
	p, err := s.Path(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(token+"\n"), fileMode); err != nil {
		t.Fatal(err)
	}
}

func TestNewStore_DirIsBesideTheConfigFile(t *testing.T) {
	for _, tc := range []struct{ configPath, want string }{
		{"config/config.yaml", filepath.Join("config", "tokens")},
		{"/app/config/config.yaml", filepath.Join("/app/config", "tokens")},
		{"config.yaml", "tokens"},
	} {
		if got := NewStore(tc.configPath).Dir; got != tc.want {
			t.Errorf("NewStore(%q).Dir = %q，期望 %q", tc.configPath, got, tc.want)
		}
	}
}

// 新位置必须落在配置目录下，而配置目录不会被挂进 Runner 容器——
// 这是本包存在的全部理由，钉住它免得日后有人把 Dir 改回 base_path 下。
func TestNewStore_DirIsNotUnderTheRunnerBasePath(t *testing.T) {
	store := NewStore("/app/config/config.yaml")
	const basePath = "/app/runners"
	rel, err := filepath.Rel(basePath, store.Dir)
	if err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("凭据目录 %s 落在了 Runner 目录 %s 之下，容器模式会把它挂进容器", store.Dir, basePath)
	}
}

// TestMigrate 覆盖 4 种组合。每条都断言「旧文件是否还在」——
// 只复制不删除的话旧文件仍在挂载目录里，漏洞没有关闭，所以那是本表的重点。
func TestMigrate(t *testing.T) {
	cases := []struct {
		name         string
		legacy       string // 空表示旧位置没有文件
		existing     string // 空表示新位置没有文件
		wantMigrated bool
		wantToken    string // 迁移后新位置应有的内容
	}{
		{name: "旧无新无：什么也不做"},
		{name: "旧无新有：什么也不做", existing: "new-pat", wantToken: "new-pat"},
		{name: "旧有新无：搬过去并删掉旧的", legacy: "old-pat", wantMigrated: true, wantToken: "old-pat"},
		{name: "旧有新有内容相同：删掉旧的", legacy: "same-pat", existing: "same-pat", wantToken: "same-pat"},
		{name: "旧有新有内容不同：以新为准，删掉旧的", legacy: "old-pat", existing: "new-pat", wantToken: "new-pat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			installDir := filepath.Join(root, "runners", "alpha")
			if err := os.MkdirAll(installDir, 0700); err != nil {
				t.Fatal(err)
			}
			store := &Store{Dir: filepath.Join(root, "config", "tokens")}
			if tc.legacy != "" {
				writeLegacy(t, installDir, tc.legacy)
			}
			if tc.existing != "" {
				writeStore(t, store, "alpha", tc.existing)
			}

			migrated, err := store.Migrate("alpha", installDir)
			if err != nil {
				t.Fatalf("Migrate 出错：%v", err)
			}
			if migrated != tc.wantMigrated {
				t.Errorf("migrated = %v，期望 %v", migrated, tc.wantMigrated)
			}
			if got := store.GitHubToken("alpha"); got != tc.wantToken {
				t.Errorf("GitHubToken = %q，期望 %q", got, tc.wantToken)
			}
			// 旧位置有过文件的，迁移后一律不能再有：它就在挂载进容器的那个目录里
			if tc.legacy != "" {
				if _, err := os.Stat(legacyPath(installDir)); !os.IsNotExist(err) {
					t.Errorf("旧位置 %s 仍然存在（err=%v），Job 还能读到它", legacyPath(installDir), err)
				}
			}
		})
	}
}

// 每个检查周期都会调用，所以必须幂等：第二次调用不该报错，也不该改变结果
func TestMigrate_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	writeLegacy(t, installDir, "old-pat")

	if migrated, err := store.Migrate("alpha", installDir); err != nil || !migrated {
		t.Fatalf("首次迁移应成功：migrated=%v err=%v", migrated, err)
	}
	migrated, err := store.Migrate("alpha", installDir)
	if err != nil {
		t.Fatalf("再次迁移不该出错：%v", err)
	}
	if migrated {
		t.Error("没有可搬的东西时 migrated 应为 false")
	}
	if got := store.GitHubToken("alpha"); got != "old-pat" {
		t.Errorf("令牌应保持 old-pat，得到 %q", got)
	}
}

// 空文件里没有凭据：没有要搬的东西，也不去删用户的文件
func TestMigrate_EmptyLegacyFileIsLeftAlone(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	writeLegacy(t, installDir, "   ")

	migrated, err := store.Migrate("alpha", installDir)
	if err != nil {
		t.Fatalf("Migrate 出错：%v", err)
	}
	if migrated {
		t.Error("空文件不该算作一次迁移")
	}
	if _, err := os.Stat(legacyPath(installDir)); err != nil {
		t.Errorf("空文件不含凭据，应原样留着：%v", err)
	}
	if got := store.GitHubToken("alpha"); got != "" {
		t.Errorf("新位置不该被写入，得到 %q", got)
	}
}

// 写入失败时旧文件必须留着：先删后写会把用户唯一的一份 PAT 弄丢。
//
// 制造失败的办法要挑一下。只读目录不行——测试常以 root 跑，root 照样写得进去，
// 那条断言会凭运气通过。让 Dir 的父路径是个普通文件也不行：那样连
// os.ReadFile(新位置) 都拿 ENOTDIR，Migrate 在「读不出新位置」那一路就返回了，
// 压根走不到写入（那一路另有 TestMigrate_UnreadableNewLocationKeepsTheLegacyFile）。
//
// 用一个指向不存在目标的符号链接：读新位置得到 ENOENT（于是继续往写入走），
// 而 MkdirAll 在这个名字上失败。两个条件同时成立，且与 UID 无关。
func TestMigrate_WriteFailureKeepsTheLegacyFile(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	writeLegacy(t, installDir, "old-pat")

	dir := filepath.Join(root, "tokens")
	if err := os.Symlink(filepath.Join(root, "nonexistent", "target"), dir); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: dir}
	// 钉住前提：必须确实是「新位置不存在」，否则这条用例测的就不是写入失败那一路
	newPath, err := store.Path("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(newPath); !os.IsNotExist(err) {
		t.Fatalf("前提不成立：读新位置应得到「不存在」，得到 %v", err)
	}

	migrated, err := store.Migrate("alpha", installDir)
	if err == nil {
		t.Fatal("写入不可能成功，Migrate 应返回错误")
	}
	if migrated {
		t.Error("写入失败时 migrated 必须为 false")
	}
	b, readErr := os.ReadFile(legacyPath(installDir))
	if readErr != nil {
		t.Fatalf("写入失败时旧文件必须留着，否则 PAT 就丢了：%v", readErr)
	}
	if strings.TrimSpace(string(b)) != "old-pat" {
		t.Errorf("旧文件内容应保持不变，得到 %q", b)
	}
}

// 新位置读不出来（不是「不存在」，而是读失败）时同样不能删旧文件：
// 不知道新位置有什么，就不能拿掉手上唯一确定存在的那一份。
func TestMigrate_UnreadableNewLocationKeepsTheLegacyFile(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	writeLegacy(t, installDir, "old-pat")

	blocker := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: filepath.Join(blocker, "tokens")}

	migrated, err := store.Migrate("alpha", installDir)
	if err == nil {
		t.Fatal("读不出新位置时 Migrate 应返回错误")
	}
	if migrated {
		t.Error("没搬成时 migrated 必须为 false")
	}
	if _, err := os.Stat(legacyPath(installDir)); err != nil {
		t.Fatalf("旧文件必须留着：%v", err)
	}
}

// 只读目录那一路也验一下，但 root 下这个前提不成立，跳过
func TestMigrate_WriteFailureOnReadOnlyDirKeepsTheLegacyFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时只读目录照样可写，这条前提不成立")
	}
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	writeLegacy(t, installDir, "old-pat")

	parent := filepath.Join(root, "ro")
	if err := os.MkdirAll(parent, 0500); err != nil {
		t.Fatal(err)
	}
	store := &Store{Dir: filepath.Join(parent, "tokens")}

	if _, err := store.Migrate("alpha", installDir); err == nil {
		t.Fatal("写入不可能成功，Migrate 应返回错误")
	}
	if _, err := os.Stat(legacyPath(installDir)); err != nil {
		t.Fatalf("写入失败时旧文件必须留着：%v", err)
	}
}

// 权限是这件事的一半：目录 0700、文件 0600
func TestMigrate_Permissions(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	writeLegacy(t, installDir, "old-pat")

	if _, err := store.Migrate("alpha", installDir); err != nil {
		t.Fatalf("Migrate 出错：%v", err)
	}
	di, err := os.Stat(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != dirMode {
		t.Errorf("凭据目录权限为 %04o，期望 %04o", got, dirMode)
	}
	p, err := store.Path("alpha")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != fileMode {
		t.Errorf("凭据文件权限为 %04o，期望 %04o", got, fileMode)
	}
}

// 迁移后不留临时文件：tokens/ 下只应有按 Runner 名命名的那一个
func TestMigrate_LeavesNoTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	installDir := filepath.Join(root, "runners", "alpha")
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	writeLegacy(t, installDir, "old-pat")

	if _, err := store.Migrate("alpha", installDir); err != nil {
		t.Fatalf("Migrate 出错：%v", err)
	}
	entries, err := os.ReadDir(store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "alpha" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("tokens/ 下应只有 alpha，得到 %v", names)
	}
}

func TestMigrate_NoInstallDirIsNotAnError(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "tokens")}
	if migrated, err := store.Migrate("alpha", ""); err != nil || migrated {
		t.Fatalf("installDir 为空应静默返回：migrated=%v err=%v", migrated, err)
	}
}

func TestGitHubToken(t *testing.T) {
	root := t.TempDir()
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	if got := store.GitHubToken("alpha"); got != "" {
		t.Errorf("没有凭据时应返回空串，得到 %q", got)
	}
	writeStore(t, store, "alpha", "  pat-with-space  ")
	if got := store.GitHubToken("alpha"); got != "pat-with-space" {
		t.Errorf("应去掉首尾空白，得到 %q", got)
	}
	if got := store.GitHubToken("beta"); got != "" {
		t.Errorf("另一个 Runner 不该读到，得到 %q", got)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}
	// 不存在时不算错误——删除 Runner 时绝大多数都没有 PAT
	if err := store.Remove("alpha"); err != nil {
		t.Fatalf("删除不存在的凭据不该出错：%v", err)
	}
	writeStore(t, store, "alpha", "pat")
	writeStore(t, store, "beta", "other")
	if err := store.Remove("alpha"); err != nil {
		t.Fatalf("Remove 出错：%v", err)
	}
	if got := store.GitHubToken("alpha"); got != "" {
		t.Errorf("alpha 的凭据应已删除，得到 %q", got)
	}
	if got := store.GitHubToken("beta"); got != "other" {
		t.Errorf("不该动到 beta，得到 %q", got)
	}
}

// 名字要拼进路径，不合法的名字必须挡在外面而不是拼出一个越界路径
func TestPath_RejectsUnsafeNames(t *testing.T) {
	store := &Store{Dir: filepath.Join(t.TempDir(), "tokens")}
	for _, name := range []string{"", "..", "../etc/passwd", "a/b", `a\b`, "  "} {
		if _, err := store.Path(name); err == nil {
			t.Errorf("名字 %q 应被拒绝", name)
		}
		if got := store.GitHubToken(name); got != "" {
			t.Errorf("名字 %q 不该读出任何东西，得到 %q", name, got)
		}
		if err := store.Remove(name); err == nil {
			t.Errorf("名字 %q 的 Remove 应返回错误", name)
		}
	}
	// 合法的名字要能过，包括以 . 开头的——正是它让 base_path/.something/ 那个方案不可行
	for _, name := range []string{"alpha", ".hidden", "a.b-c_d"} {
		if !config.IsSafeRunnerNameOrPath(name) {
			t.Fatalf("前提有变：%q 现在被 config 判为不合法", name)
		}
		if _, err := store.Path(name); err != nil {
			t.Errorf("名字 %q 应被接受：%v", name, err)
		}
	}
}

func TestMigrateAll(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "runners")
	store := &Store{Dir: filepath.Join(root, "config", "tokens")}

	cfg := &config.Config{}
	cfg.Runners.BasePath = basePath
	cfg.Runners.Items = []config.RunnerItem{
		{Name: "alpha", TargetType: "repo", Target: "o/r"},
		{Name: "beta", TargetType: "repo", Target: "o/r"},
		{Name: "gamma", TargetType: "repo", Target: "o/r", Path: "gamma-dir"},
	}
	// alpha 与 gamma 各有一个旧位置的 PAT，beta 没有
	writeLegacy(t, filepath.Join(basePath, "alpha"), "alpha-pat")
	writeLegacy(t, filepath.Join(basePath, "gamma-dir"), "gamma-pat")

	if moved := MigrateAll(cfg, store); moved != 2 {
		t.Errorf("应搬动 2 个，得到 %d", moved)
	}
	if got := store.GitHubToken("alpha"); got != "alpha-pat" {
		t.Errorf("alpha 的令牌为 %q", got)
	}
	// path 与 name 不同的 Runner 也要处理：旧文件在 path 下，新文件按 name 寻址
	if got := store.GitHubToken("gamma"); got != "gamma-pat" {
		t.Errorf("gamma 的令牌为 %q", got)
	}
	if got := store.GitHubToken("beta"); got != "" {
		t.Errorf("beta 本来就没有 PAT，得到 %q", got)
	}
	for _, dir := range []string{"alpha", "gamma-dir"} {
		if _, err := os.Stat(legacyPath(filepath.Join(basePath, dir))); !os.IsNotExist(err) {
			t.Errorf("%s 的旧文件仍然存在（err=%v）", dir, err)
		}
	}
	if moved := MigrateAll(nil, store); moved != 0 {
		t.Errorf("cfg 为 nil 时应返回 0，得到 %d", moved)
	}
	if moved := MigrateAll(cfg, nil); moved != 0 {
		t.Errorf("store 为 nil 时应返回 0，得到 %d", moved)
	}
}
