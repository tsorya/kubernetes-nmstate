package configurations

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/nmstate/kubernetes-nmstate/pkg/helper"
	"github.com/thoas/go-funk"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	nnscontroller "github.com/nmstate/kubernetes-nmstate/pkg/controller/nodenetworkstate"
	"github.com/spf13/viper"
)

const defaultInterfacesFilter = "veth*"
const defaultNodeNetworkStateRefresh = "5"

var (
	log        = logf.Log.WithName("configurations")
	configPath string
)

// Takes os environment values
func init() {
	configPathTemp, isSet := os.LookupEnv("CONFIG_PATH")
	if !isSet {
		panic("CONFIG_PATH is mandatory")
	}
	configPath = configPathTemp
}

// Struct to match config file
type Config struct {
	NodeNetworkRefreshInterval string `mapstructure:"node_network_state_refresh_interval"`
	InterfaceFilter            string `mapstructure:"interfaces_filter"`
}

// Init defaults
var c = &Config{
	NodeNetworkRefreshInterval: defaultNodeNetworkStateRefresh,
	InterfaceFilter:            defaultInterfacesFilter,
}

var v = viper.New()

// Updating relevant values with new config settings
func (c *Config) updateConfig(v viper.Viper) {
	err := v.Unmarshal(&c)
	if err != nil {
		log.Error(err, "Failed to unmarshal config file")
		return
	}
	log.Info("Updating configs")
	// Initializing client with new filter if not set in config will use default
	helper.SetInterfacesFilter(c.InterfaceFilter)
	// Initializing node state controller with new refresh interval
	nnscontroller.SetNodeNetworkStateRefreshValue(c.NodeNetworkRefreshInterval)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func ConfigWatcher() error {

	ext := filepath.Ext(configPath)
	if !funk.Contains(viper.SupportedExts, ext[1:]) {
		return fmt.Errorf("file extension %s is not supported", ext)
	}

	configDir, _ := filepath.Split(configPath)
	if !exists(configDir) {
		return fmt.Errorf("folder %s doesn't exist, can't start configuration watcher", configDir)
	}

	v.SetConfigFile(configPath)
	v.SetTypeByDefaultValue(true)

	if err := v.ReadInConfig(); err != nil { // Find and read the config file
		log.Info("Not able to read configuration, will update default values")
	}

	c.updateConfig(*v)
	WatchConfig(v, configPath)
	return nil
}

//Adapted from viper WatchConfig to match nmstate configmap watch needs
//The main changes is that there is no need to
//preexist config file before starting the watch
//and it will not exit on file deletion
func WatchConfig(v *viper.Viper, fullFilePath string) {
	initWG := sync.WaitGroup{}
	initWG.Add(1)
	go func() {
		newWatcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Error(err, "Failed to start fsnotify watcher")
			return
		}
		defer newWatcher.Close()

		configFile := filepath.Clean(fullFilePath)
		configDir, _ := filepath.Split(configFile)
		realConfigFile, _ := filepath.EvalSymlinks(fullFilePath)

		eventsWG := sync.WaitGroup{}
		eventsWG.Add(1)
		go func() {
			for {
				select {
				case event, ok := <-newWatcher.Events:
					if !ok { // 'Events' channel is closed
						eventsWG.Done()
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
							c.updateConfig(*v)
						}
					}
				case err, ok := <-newWatcher.Errors:
					if ok { // 'Errors' channel is not closed
						log.Error(err, "newWatcher error\n")
					}
					eventsWG.Done()
					return
				}
			}
		}()
		newWatcher.Add(configDir)
		initWG.Done()   // done initalizing the watch in this go routine, so the parent routine can move on...
		eventsWG.Wait() // now, wait for event loop to end in this go-routine...
	}()
	initWG.Wait() // make sure that the go routine above fully ended before returning
}
