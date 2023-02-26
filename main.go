package main

import (
	"io/ioutil"
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
	if strings.HasPrefix(name, "/") && !strings.HasPrefix(name, devDirectory+"/") {
		return false
	}
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

	deviceChanged := func() {
		log.Infof("Checking for references to video devices.")
		processes, err := ioutil.ReadDir("/proc")
		if err != nil {
			log.Errorf("Error reading /proc: %s", err)
			return
		}
		for _, process := range processes {
			if !process.IsDir() {
				continue
			}
			processID := process.Name()
			fds, err := ioutil.ReadDir(path.Join("/proc", processID, "fd"))
			if err != nil {
				continue
			}
			for _, fd := range fds {
				link, err := os.Readlink(path.Join("/proc", processID, "fd", fd.Name()))
				if err != nil {
					continue
				}
				if isVideoDevice(link) {
					log.WithFields(log.Fields{"device": link, "process": processID}).Infof("Found reference to device.")
					deviceOpened()
					return
				}
			}
		}
		log.Infof("No references to video devices found.")
		deviceClosed()
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
				log.WithFields(log.Fields{"device": event.Name, "mask": event.Mask}).Info("Device opened.")
				debounced(func() {
					deviceChanged()
				})
			}
			if event.Mask&inotify.InClose != 0 {
				log.WithFields(log.Fields{"device": event.Name, "mask": event.Mask}).Info("Device closed.")
				debounced(func() {
					deviceChanged()
				})
			}
			if event.Mask&inotify.InCreate != 0 && isVideoDevice(event.Name) {
				logWithFields := log.WithFields(log.Fields{"device": event.Name})
				logWithFields.Info("Device detected.")
				attempts := 0
				for {
					watcher.RemoveWatch(event.Name)
					err := watcher.AddWatch(event.Name, deviceUsageMask)
					if err == nil {
						logWithFields.Info("Added watch for device.")
						break
					} else {
						attempts++
						if attempts > 5 {
							logWithFields.Errorf("%+v", err)
							break
						} else {
							logWithFields.Warnf("%+v", err)
							time.Sleep(1 * time.Second)
							logWithFields.Info("Retrying...")
						}
					}
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
