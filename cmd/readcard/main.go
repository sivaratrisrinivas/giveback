// Command readcard reads an invitation image and prints who is inviting.
// It must run on a Sprite that has access to the model connectors.
//
//	readcard [-force-fallback] card.jpg [back.jpg ...]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/sivaratrisrinivas/giveback/internal/card"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

func main() {
	forceFallback := flag.Bool("force-fallback", false, "skip Gemini to test the Claude fallback")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: readcard [-force-fallback] card.jpg [more.jpg ...]")
	}
	var imgs []vision.Image
	for _, p := range flag.Args() {
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		imgs = append(imgs, vision.Image{MIME: http.DetectContentType(b), Data: b})
	}
	r := vision.New()
	if *forceFallback {
		os.Setenv("GIVEBACK_GEMINI_BASE", "http://127.0.0.1:1") // unreachable on purpose
		r = vision.New()
	}
	inv, res, err := card.Read(context.Background(), r, imgs...)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("model: %s  time: %s\n", res.Model, res.Elapsed.Round(1e6))
	if res.PrimaryErr != nil {
		fmt.Printf("gemini skipped: %v\n", res.PrimaryErr)
	}
	out, _ := json.MarshalIndent(inv, "", "  ")
	fmt.Println(string(out))
	fmt.Println("hosts to look up:")
	for _, h := range inv.Hosts() {
		fmt.Printf("  - %s / %s (surname %q, deceased=%v)\n", h.NameEnglish, h.NameTelugu, h.Surname, h.Deceased)
	}
}
