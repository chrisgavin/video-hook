package main

import (
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/bep/debounce"
	log "github.com/sirupsen/logrus"
	"k8s.io/utils/inotify"
)

const devDirectory = "/dev"
const deviceDetectionMask = inotify.InCreate
const deviceUsageMask = inotify.InOpen | inotify.InClose

func isVideoDevice(name string) bool {
	_, basename := path.Split(name)
	return strings.HasPrefix(basename, "video")
}

func executeScript(script string, environment []string) {
	command := exec.Command(script)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = os.Environ()
	command.Env = append(command.Env, environment...)
	err := command.Run()
	if err != nil {
		log.Errorf("Error running script: %s", err)
	}
}

func run() error {
	scripts := os.Args[1:]
	if len(scripts) == 0 {
		log.Warn("No scripts provided. Video device access will only be logged.")
	}

	deviceOpened := func() {
		log.Infof("Triggering device opened hook.")
		for _, script := range scripts {
			log.Infof("Running script %s...", script)
			executeScript(script, []string{"ACTION=OPEN"})
		}
	}

	deviceClosed := func() {
		log.Infof("Triggering device closed hook.")
		for _, script := range scripts {
			log.Infof("Running script %s...", script)
			executeScript(script, []string{"ACTION=CLOSE"})
		}
	}

	watcher, err := inotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	err = watcher.AddWatch(devDirectory, deviceDetectionMask)
	if err != nil {
		return err
	}
	fileInfos, err := os.ReadDir(devDirectory)
	if err != nil {
		return err
	}
	for _, fileInfo := range fileInfos {
		if !isVideoDevice(fileInfo.Name()) {
			continue
		}
		err = watcher.AddWatch(path.Join(devDirectory, fileInfo.Name()), deviceUsageMask)
		if err != nil {
			return err
		}
	}

	debounced := debounce.New(500 * time.Millisecond)
	for {
		select {
		case event := <-watcher.Event:
			if event.Mask&inotify.InOpen != 0 {
				log.WithFields(log.Fields{"device": event.Name}).Info("Device opened.")
				debounced(deviceOpened)
			}
			if event.Mask&inotify.InClose != 0 {
				log.WithFields(log.Fields{"device": event.Name}).Info("Device closed.")
				debounced(deviceClosed)
			}
			if event.Mask&inotify.InCreate != 0 && isVideoDevice(event.Name) {
				log.WithFields(log.Fields{"device": event.Name}).Info("Device detected.")
				err := watcher.AddWatch(event.Name, deviceUsageMask)
				if err != nil {
					log.Errorf("%+v", err)
				}
			}
		case err := <-watcher.Error:
			log.Errorf("%+v", err)
		}
	}
}

func main() {
	err := run()
	if err != nil {
		log.Fatalf("%+v", err)
	}
}
