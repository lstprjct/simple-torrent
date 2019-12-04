package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/radovskyb/watcher"
)

func (s *Server) backgroundRoutines() {

	go s.fetchSearchConfig()

	//poll torrents and files
	go func() {
		for {
			s.state.Lock()
			s.state.Torrents = s.engine.GetTorrents()
			s.state.Downloads = s.listFiles()
			s.state.Unlock()
			s.state.Push()
			time.Sleep(3 * time.Second)
		}
	}()
	// slow update on debug info
	go func() {
		var sBuf bytes.Buffer
		sWriter := bufio.NewWriter(&sBuf)
		for {
			time.Sleep(30 * time.Second)
			sBuf.Reset()
			s.engine.WriteStauts(sWriter)
			s.state.Lock()
			s.state.EngineStatus = sBuf.String()
			s.state.Unlock()
			s.state.Push()
		}
	}()
	//start collecting stats
	go func() {
		for {
			c := s.engine.Config()
			s.state.Stats.System.loadStats(c.DownloadDirectory)
			time.Sleep(5 * time.Second)
		}
	}()
	go func() {
		for {
			s.updateRSS()
			time.Sleep(30 * time.Minute)
		}
	}()

	go func() {
		s.engine.UpdateTrackers()

		if w, err := os.Stat(s.state.Config.WatchDirectory); os.IsNotExist(err) || (err == nil && !w.IsDir()) {
			log.Printf("[Watcher] %s is not dir", s.state.Config.WatchDirectory)
			return
		}

		time.Sleep(time.Second * 10)

		// restore saved torrent tasks
		tors, _ := filepath.Glob(filepath.Join(s.state.Config.WatchDirectory, "*.torrent"))
		for _, t := range tors {
			if err := s.engine.NewFileTorrent(t); err == nil {
				if strings.HasPrefix(filepath.Base(t), cacheSavedPrefix) {
					log.Printf("Inital Task Restored: %s \n", t)
				} else {
					log.Printf("Inital Task: added %s, file removed\n", t)
					os.Remove(t)
				}
			} else {
				log.Printf("Inital Task: fail to add %s, ERR:%#v\n", t, err)
			}
		}

		// restore saved magnet tasks
		infos, _ := filepath.Glob(filepath.Join(s.state.Config.WatchDirectory, "*.info"))
		for _, i := range infos {
			fn := filepath.Base(i)
			if strings.HasPrefix(fn, cacheSavedPrefix) && len(fn) == 59 {
				mag, err := ioutil.ReadFile(i)
				if err != nil {
					continue
				}
				if err := s.engine.NewMagnet(string(mag)); err == nil {
					log.Printf("Inital Task Restored: %s \n", fn)
				} else {
					log.Printf("Inital Task: fail to add %s, ERR:%#v\n", fn, err)
				}
			}
		}
	}()
}

func (s *Server) torrentWatcher() error {

	if s.watcher != nil {
		log.Print("Torrent Watcher: close")
		s.watcher.Close()
		s.watcher = nil
	}

	if w, err := os.Stat(s.state.Config.WatchDirectory); os.IsNotExist(err) || (err == nil && !w.IsDir()) {
		return fmt.Errorf("[Watcher] %s is not dir", s.state.Config.WatchDirectory)
	}

	log.Printf("Torrent Watcher: watching torrent file in %s", s.state.Config.WatchDirectory)
	w := watcher.New()
	w.SetMaxEvents(10)
	w.FilterOps(watcher.Create)

	go func() {
		for {
			select {
			case event := <-w.Event:
				if event.IsDir() {
					continue
				}
				// skip auto saved torrent
				if strings.HasPrefix(event.Name(), cacheSavedPrefix) {
					continue
				}
				if strings.HasSuffix(event.Name(), ".torrent") {
					if err := s.engine.NewFileTorrent(event.Path); err == nil {
						log.Printf("Torrent Watcher: added %s, file removed\n", event.Name())
						os.Remove(event.Path)
					} else {
						log.Printf("Torrent Watcher: fail to add %s, ERR:%#v\n", event.Name(), err)
					}
				}
			case err := <-w.Error:
				log.Print(err)
			case <-w.Closed:
				return
			}
		}
	}()

	// Watch this folder for changes.
	if err := w.Add(s.state.Config.WatchDirectory); err != nil {
		return err
	}

	s.watcher = w
	go w.Start(time.Second * 5)
	return nil
}
