package generate

import (
	"fmt"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/ledgerwatch/turbo-geth/common"
	"github.com/ledgerwatch/turbo-geth/log"
	trnt "github.com/ledgerwatch/turbo-geth/torrent"
	"os"
	"os/signal"
	"time"
)

func Seed(pathes []string) error {
	cfg:=torrent.NewDefaultClientConfig()
	if len(pathes) ==  0 {
		cfg.DataDir = "/media/b00ris/nvme/snapshots/"
		pathes=[]string{
			cfg.DataDir+"headers3/",
			//cfg.DataDir+"bodies/",
			//cfg.DataDir+"state/",
			//cfg.DataDir+"receipts/",
		}
	}
	cfg.Seed=true
	cfg.NoDHT=true
	cfg.DisableTrackers=false

	//cfg.Logger=cfg.Logger.FilterLevel(trlog.Info)
	cl,err:=torrent.NewClient(cfg)
	if err!=nil {
		return err
	}
	defer cl.Close()

	torrents:=make([]*torrent.Torrent, len(pathes))
	for i,v :=range pathes {
		i:=i
		fmt.Println("i", i)
		mi := &metainfo.MetaInfo{
			CreationDate: time.Now().Unix(),
			CreatedBy: "turbogeth",
			AnnounceList: trnt.Trackers,
		}

		info := metainfo.Info{PieceLength: 16  * 1024}
		fmt.Println("BuildFromFilePath")
		if _, err := os.Stat(v); os.IsNotExist(err) {
			fmt.Println(err)
			continue
		} else {
			fmt.Println(err)
		}
		err := info.BuildFromFilePath(v)
		if err!=nil {
			return err
		}
		mi.InfoBytes, err = bencode.Marshal(info)
		fmt.Println("AddTorrent")
		torrents[i],err = cl.AddTorrent(mi)
		if err!=nil {
			return err
		}
		if !torrents[i].Seeding() {
			log.Warn(torrents[i].Name()+" not seeding")
		}
		fmt.Println("VerifyData")
		torrents[i].VerifyData()
		go func() {
			tt:=time.Now()
			peerID:=cl.PeerID()
			fmt.Println(mi.Magnet("headers",mi.HashInfoBytes()).String())
			for {
				fmt.Println(common.Bytes2Hex(peerID[:]),torrents[i].Name(),torrents[i].InfoHash(), torrents[i].PeerConns(),"Swarm", len(torrents[i].KnownSwarm()), torrents[i].Seeding(), time.Since(tt))
				//fmt.Println("magnet", mi.Magnet("headers",mi.HashInfoBytes()).String())
				time.Sleep(time.Second*10)
			}
		}()
	}

	c:=make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	<-c
	return nil
}



var trackers = [][]string{
	{
		"udp://tracker.openbittorrent.com:80",
		"udp://tracker.publicbt.com:80",
		"udp://coppersurfer.tk:6969/announce",
		"udp://open.demonii.com:1337",
		"udp://tracker.istole.it:6969",
		"http://bttracker.crunchbanglinux.org:6969/announce",
	},
}