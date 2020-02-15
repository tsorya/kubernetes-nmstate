package configurations

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/thoas/go-funk"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"github.com/spf13/viper"
)

const defaultInterfacesFilter = "veth*"
const defaultNodeNetworkStateRefresh = 5

var (
	log   = logf.Log.WithName("configurations")
	mutex = sync.Mutex{}
)

// Struct to match config file
type config struct {
	NodeNetworkRefreshInterval int    `mapstructure:"node_network_state_refresh_interval"`
	InterfaceFilter            string `mapstructure:"interfaces_filter"`
}

var c = config{
	NodeNetworkRefreshInterval: defaultNodeNetworkStateRefresh,
	InterfaceFilter:            defaultInterfacesFilter,
}

type configWatcher struct {
	configPath string
	v          *viper.Viper
}

func NewConfigWatcher() *configWatcher {
	configPathTemp, isSet := os.LookupEnv("CONFIG_PATH")
	if !isSet {
		panic("CONFIG_PATH is mandatory")
	}
	cw := configWatcher{
		configPath: configPathTemp,
	}
	cw.v = viper.New()
	return &cw
}

func GetCurrentConfig() config {
	return c
}

// Updating relevant values with new config settings
func setConfig(v viper.Viper) {
	mutex.Lock()
	log.Info("Updating configuration")
	err := v.Unmarshal(&c)
	mutex.Unlock()
	log.Info(fmt.Sprintf("New configuration is %+v", c))
	if err != nil {
		log.Error(err, "Failed to unmarshal config file")
		return
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}

// This function reads current config file if it is exists.
// If file exists it will update config values with values from it.
// If not it will update config with default values
// And after that it starts configuration watcher go routine
func (c *configWatcher) Start() error {
	ext := filepath.Ext(c.configPath)
	if !funk.Contains(viper.SupportedExts, ext[1:]) {
		return fmt.Errorf("file extension %s is not supported", ext)
	}

	configDir, _ := filepath.Split(c.configPath)
	if !pathExists(configDir) {
		return fmt.Errorf("folder %s doesn't exist, can't start configuration watcher", configDir)
	}

	c.v.SetConfigFile(c.configPath)
	c.v.SetTypeByDefaultValue(true)

	if err := c.v.ReadInConfig(); err != nil { // Find and read the config file
		log.Info("Not able to read configuration, will update default values")
	}

	setConfig(*c.v)
	watchConfigFile(c.v, c.configPath)
	return nil
}

// Adapted from viper WatchConfig to match nmstate configmap watch needs
// The main changes is that there is no need to
// preexist config file before starting the watch
// and it will not exit on file deletion
func watchConfigFile(v *viper.Viper, fullFilePath string) {
	newWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Error(err, "Failed to start fsnotify watcher")
		return
	}
	defer newWatcher.Close()

	configFile := filepath.Clean(fullFilePath)
	configDir, _ := filepath.Split(configFile)
	realConfigFile, _ := filepath.EvalSymlinks(fullFilePath)
	go func() {
		for {
			select {
			case event, ok := <-newWatcher.Events:
				if !ok { // 'Events' channel is closed
					return
				}
				currentConfigFile, _ := filepath.EvalSymlinks(fullFilePath)
				// we only care about the config file with the following cases:
				// 1 - if the config file was modified or created
				// 2 - if the real path to the config file changed (eg: k8s ConfigMap replacement)
				const writeOrCreateMask = fsnotify.Write | fsnotify.Create
				if (filepath.Clean(event.Name) == configFile &&
					event.Op&writeOrCreateMask != 0) ||
					(currentConfigFile != "" && currentConfigFile != realConfigFile) {
					realConfigFile = currentConfigFile
					err := v.ReadInConfig()
					if err != nil {
						log.Error(err, "error reading config file.\n")
					} else {
						setConfig(*v)
					}
				}
			case err, ok := <-newWatcher.Errors:
				if ok { // 'Errors' channel is not closed
					log.Error(err, "newWatcher error\n")
				}
				return
			}
		}
	}()
	newWatcher.Add(configDir)
}
