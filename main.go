package main

import (
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"time"
	"code.google.com/p/bencode-go"
)

type Files struct {
	Length int "length"
	Path []string "path"
}

type Info struct {
	Name string "name"
	Length int "length"
//	Files []Files "files"
	Pieces string "pieces"
	PieceLength int "piece length"
}

type Metainfo struct {
	Info Info "info"
	Announce string "announce"
	AnnounceList [][]string "announce-list"
}

var PeerId = [20]byte {
	'-',
	'T',
	'V',
	'0',
	'0',
	'0',
	'1',
}

func init() {
	// Initialize PeerId
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 7; i < 20; i++ {
		PeerId[i] = byte(r.Intn(256))
	}
}

func main() {
	var m Metainfo
	var announce_url *url.URL

	if len(os.Args) != 2 {
		log.Fatalf("Usage: %s: torrent\n", os.Args[0])
        }
	torrent := os.Args[1]

	file, err := os.Open(torrent)
	if err != nil {
		log.Fatal(err)
	}

	err = bencode.Unmarshal(file, &m)
	if err != nil {
		log.Fatal(err)
	}

	if (m.Info.Length != 0) {
		log.Println("Single File Mode")
		fmt.Println(m.Info.Length)
	} else {
		log.Fatal("Multiple File Mode not implemented")
	}

	fmt.Println(m.Announce)

	h := sha1.New()

	err = bencode.Marshal(h, m.Info)
	if err != nil {
		log.Fatal(err)
	}
	info_hash := h.Sum(nil)
	fmt.Println(info_hash)

	announce_url, err = url.Parse(m.Announce)
	if (err != nil) {
		log.Fatal(err)
	}
	fmt.Println(announce_url)

	// statically set these for now
	port := "6881"
	downloaded := "0"
	uploaded := "0"

	tracker_request := url.Values{}
	tracker_request.Set("info_hash", string(info_hash))
	tracker_request.Add("peer_id", string(PeerId[:]))
	tracker_request.Add("port", port)
	tracker_request.Add("uploaded", uploaded)
	tracker_request.Add("downloaded", downloaded)
	tracker_request.Add("left", string(m.Info.Length))
	tracker_request.Add("compact", "1")
	announce_url.RawQuery = tracker_request.Encode()

	resp, err := http.Get(announce_url.String())
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", body)
}