package kinko

const (
	cmdInit      = "init"
	cmdUnlock    = "unlock"
	cmdLock      = "lock"
	cmdStatus    = "status"
	cmdBackup    = "backup"
	cmdRestore   = "restore"
	cmdVersion   = "version"
	cmdSet       = "set"
	cmdSetKey    = "set-key"
	cmdDelete    = "delete"
	cmdCopy      = "copy"
	cmdMove      = "move"
	cmdExplosion = "explosion"
	cmdGet       = "get"
	cmdShow      = "show"
	cmdConfig    = "config"
	cmdExport    = "export"
	cmdImport    = "import"
	cmdExec      = "exec"
	cmdFolder    = "folder"
	cmdProfile   = "profile"
	cmdPath      = "path"
	cmdPassword  = "password"
	cmdDirenv    = "direnv"
	cmdDoctor    = "doctor"
	cmdMigration = "migration"
	cmdSync      = "sync"
)

const (
	cmdSyncPush = "push"
	cmdSyncPull = "pull"
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
	folderAdd    = "add"
	folderUnlock = "unlock"
	folderLock   = "lock"
	folderRemove = "remove"
	folderStatus = "status"
	folderPath   = "path"
)

const (
	copyLocalToLocal  = "local-to-local"
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
