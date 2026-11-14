package kinko

const (
	cmdInit      = "init"
	cmdUnlock    = "unlock"
	cmdLock      = "lock"
	cmdStatus    = "status"
	cmdBackup    = "backup"
	cmdVersion   = "version"
	cmdSet       = "set"
	cmdSetKey    = "set-key"
	cmdDelete    = "delete"
	cmdMove      = "move"
	cmdExplosion = "explosion"
	cmdGet       = "get"
	cmdShow      = "show"
	cmdConfig    = "config"
	cmdExport    = "export"
	cmdImport    = "import"
	cmdExec      = "exec"
	cmdProfile   = "profile"
	cmdPath      = "path"
	cmdPassword  = "password"
	cmdDirenv    = "direnv"
)

const (
	configShow = "show"
	configSet  = "set"
)

const (
	profileList = "list"
)

const (
	pathPruneMissing = "prune-missing"
)

const (
	moveLocalToShared = "local-to-shared"
	moveSharedToLocal = "shared-to-local"
)

const (
	shellPosix   = "posix"
	shellSh      = "sh"
	shellBash    = "bash"
	shellZsh     = "zsh"
	shellFish    = "fish"
	shellNu      = "nu"
	shellNushell = "nushell"
)
