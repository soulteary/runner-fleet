// Package secrets 管理只属于 Manager 的凭据（目前只有可选的 GitHub PAT）。
//
// 它们不能放在 runners/<name>/ 下：容器模式会把那个目录整个挂进 Runner 容器的
// /runner，Job 以同一个 UID（1001）运行，文件权限挡不住——`chmod 600` 的属主
// 正是 Job 自己。于是任何能往这个仓库推 workflow 的人，一个
// `cat /runner/.github_check_token` 就能拿到这个 PAT，而组织目标的 PAT 带
// admin:org，等于组织管理员权限。
//
// 新位置取配置文件所在目录下的 tokens/。选它的理由是它只会挂进 Manager，
// 从来不会挂进 Runner 容器，而且本来就在文档的备份清单里。
//
// 没有选 base_path/.something/：Runner 名允许以 . 开头，一个恰好叫这个名字的
// Runner 会让它的安装目录就是密钥目录，而那个目录是会被挂进容器的。
package secrets

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/soulteary/runner-fleet/internal/config"
)

// LegacyRunnerTokenFile 是 PAT 的旧位置：各 Runner 安装目录下的这个文件。
//
// 只保留给迁移用。本包之外不应再出现它——读 PAT 一律走 Store，
// 由 TestNoManagerSecretsUnderRunnerDirs 守着。
const LegacyRunnerTokenFile = ".github_check_token"

// tokensDirName 配置目录下存放凭据的子目录名
const tokensDirName = "tokens"

// PathDescription 新位置的通用写法，用在拿不到具体路径的消息里。
// 实际位置随 -config 而变，按 compose 的默认挂载就是这个。
const PathDescription = "config/tokens/<runner name>"

// dirMode / fileMode 密钥目录与密钥文件的权限
const (
	dirMode  os.FileMode = 0700
	fileMode os.FileMode = 0600
)

// Store 按 Runner 名寻址 Manager 侧的凭据。
// Dir 为 <配置文件所在目录>/tokens，按 compose 的默认挂载即 /app/config/tokens。
type Store struct{ Dir string }

// NewStore 由 -config 的路径推出凭据目录。
// 传 config/config.yaml 得到 config/tokens。
func NewStore(configPath string) *Store {
	return &Store{Dir: filepath.Join(filepath.Dir(configPath), tokensDirName)}
}

// Path 返回某个 Runner 的凭据文件路径，名字不合法时返回错误。
//
// 这里重新校验一次，而不是信赖调用方：名字要拼进路径，而
// config.IsSafeRunnerNameOrPath 挡的正是 / \ 与 ..。配置加载时已经校验过，
// 但一个拼路径的地方自己不校验，就得指望每一条调用链上都没人漏掉。
func (s *Store) Path(runnerName string) (string, error) {
	name := strings.TrimSpace(runnerName)
	if !config.IsSafeRunnerNameOrPath(name) {
		return "", fmt.Errorf("runner name %q cannot be used as a file name (.. / \\ are not allowed)", runnerName)
	}
	return filepath.Join(s.Dir, name), nil
}

// GitHubToken 读取该 Runner 的 PAT。空串表示没有——
// 与旧的 TokenForRunner 一致：没有凭据不是错误，本工具不要求这个凭据。
func (s *Store) GitHubToken(runnerName string) string {
	p, err := s.Path(runnerName)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Migrate 把旧位置（Runner 目录下）的 PAT 搬到新位置，并删除旧文件。
//
// migrated 为 true 表示这次确实把一个 PAT 写进了新位置。删除旧文件而不只是复制，
// 是这件事的全部意义所在：旧文件还在挂载目录里，漏洞就没有关闭。
//
// 每个检查周期都会调用，所以它必须在「没什么可做」时足够便宜，并且幂等。
// 也正因为每个周期都跑，用户照旧文档把 PAT 放进 Runner 目录的，最迟一个周期后
// 也会被移走。
//
// 写入新位置失败时不删除旧文件，并把错误返回给调用方：先删后写会丢掉用户的 PAT。
func (s *Store) Migrate(runnerName, installDir string) (migrated bool, err error) {
	if installDir == "" {
		return false, nil
	}
	legacy := filepath.Join(installDir, LegacyRunnerTokenFile)
	b, err := os.ReadFile(legacy)
	if err != nil {
		// 不存在是常态（绝大多数 Runner 没有 PAT），不是错误
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("cannot read the legacy PAT %s: %w", legacy, err)
	}
	old := strings.TrimSpace(string(b))
	if old == "" {
		// 空文件里没有凭据，没有要搬的东西，也没有要关闭的暴露。
		// 不去删它：这个文件不是本工具写的，删掉一个什么都不含的用户文件没有收益。
		return false, nil
	}

	newPath, err := s.Path(runnerName)
	if err != nil {
		return false, err
	}
	existing, readErr := os.ReadFile(newPath)
	switch {
	case readErr == nil && strings.TrimSpace(string(existing)) == old:
		// 两边一样：新位置已经有了，只需要把挂载目录里的那份去掉
		if err := os.Remove(legacy); err != nil {
			return false, fmt.Errorf("cannot remove the legacy PAT %s: %w", legacy, err)
		}
		return false, nil
	case readErr == nil:
		// 两边都有且不同：以新位置为准。旧文件是暴露面，照样要删掉，
		// 但要说清楚删掉的那份和保留的那份内容不同，否则用户会以为自己刚放进去的生效了。
		if err := os.Remove(legacy); err != nil {
			return false, fmt.Errorf("cannot remove the legacy PAT %s: %w", legacy, err)
		}
		log.Printf("warning: runner %s had a PAT in both %s and its runner directory, and the two differ. "+
			"Kept the one in %s and removed the one in the runner directory, which container mode mounts into the runner container. "+
			"If the removed one was the current PAT, write it to %s", runnerName, newPath, newPath, newPath)
		return false, nil
	case !os.IsNotExist(readErr):
		return false, fmt.Errorf("cannot read the PAT %s: %w", newPath, readErr)
	}

	// 新位置还没有：写进去，写成功了再删旧的
	if err := s.write(newPath, old); err != nil {
		return false, err
	}
	if err := os.Remove(legacy); err != nil {
		return false, fmt.Errorf("wrote the PAT to %s but cannot remove the legacy copy %s: %w", newPath, legacy, err)
	}
	log.Printf("moved the PAT for runner %s out of its runner directory to %s", runnerName, newPath)
	return true, nil
}

// Remove 删除该 Runner 的凭据，删除 Runner 时调用。不存在不算错误。
func (s *Store) Remove(runnerName string) error {
	p, err := s.Path(runnerName)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove the PAT %s: %w", p, err)
	}
	return nil
}

// write 原子地写入一个 0600 的凭据文件。
//
// 先写同目录下的临时文件再改名：直接写正式名字的话，写到一半被打断会留下一个
// 内容不全的 PAT，而调用方读到的是一个语法上像令牌、实际上不能用的字符串。
// os.CreateTemp 建出来就是 0600，所以中间态也不会比正式文件宽松。
func (s *Store) write(path, token string) error {
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return fmt.Errorf("cannot create the token directory %s: %w", s.Dir, err)
	}
	f, err := os.CreateTemp(s.Dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary file in %s: %w", s.Dir, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(append([]byte(token), '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %w", tmp, err)
	}
	// CreateTemp 已经是 0600，这里显式再设一次，免得受 umask 之外的因素影响
	if err := os.Chmod(tmp, fileMode); err != nil {
		return fmt.Errorf("cannot set the mode of %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot publish the PAT to %s: %w", path, err)
	}
	return nil
}

// MigrateAll 对配置里的每个 Runner 迁移一次，返回搬动的数量。
// 启动时调用，让已部署的旧路径 PAT 不必等到第一个检查周期。
func MigrateAll(cfg *config.Config, store *Store) int {
	if cfg == nil || store == nil {
		return 0
	}
	moved := 0
	for _, item := range cfg.Runners.Items {
		ok, err := store.Migrate(item.Name, item.InstallPath(cfg.Runners.BasePath))
		if err != nil {
			// 迁移失败不阻止启动：PAT 只影响可选的可见性检查与删除时的注销。
			// 但必须说出来——旧文件还在挂载目录里，那是本包要关掉的那个暴露。
			log.Printf("warning: cannot move the PAT for runner %s out of its runner directory: %v", item.Name, err)
			continue
		}
		if ok {
			moved++
		}
	}
	return moved
}
