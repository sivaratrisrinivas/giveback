// Command readpage reads notebook page photos, saves a crop of every line, and
// scores the result against the answer key. It must run on a Sprite that has
// access to the model connectors.
//
//	readpage [-key docs/synthetic-notebook.md] [-out crops/] [-force-fallback] page1.jpeg page2.jpeg ...
//
// The answer-key page for each photo is the first number in its file name.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

var pageNum = regexp.MustCompile(`\d+`)

func main() {
	keyPath := flag.String("key", "docs/synthetic-notebook.md", "answer key")
	outDir := flag.String("out", "crops", "where to save line crops")
	forceFallback := flag.Bool("force-fallback", false, "skip Gemini to test the Claude fallback")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("usage: readpage [-key key.md] [-out dir] page1.jpeg ...")
	}
	kf, err := os.Open(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	key, err := notebook.ParseKey(kf)
	kf.Close()
	if err != nil {
		log.Fatal(err)
	}
	if *forceFallback {
		os.Setenv("GIVEBACK_GEMINI_BASE", "http://127.0.0.1:1") // unreachable on purpose
	}
	r := vision.New()
	// Answers are cached by photo hash: re-running on the same photos makes no model calls.
	cacheDir := filepath.Join(*outDir, "cache")
	models := notebook.Models{
		Page:    vision.Cached(r, cacheDir, "hydrogen-first"),
		Amounts: vision.Cached(r.Haiku(), cacheDir, "haiku"),
	}

	type row struct {
		name  string
		page  notebook.Page
		score notebook.PageScore
		total int
	}
	var rows []row
	for _, path := range flag.Args() {
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		n, _ := strconv.Atoi(pageNum.FindString(name))
		kp, ok := key[n]
		if !ok {
			log.Fatalf("%s: no answer-key page %d", path, n)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		p, err := notebook.Read(context.Background(), models, vision.Image{MIME: http.DetectContentType(b), Data: b})
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}
		if err := saveCrops(filepath.Join(*outDir, name), p); err != nil {
			log.Fatal(err)
		}
		s := notebook.Score(p, kp)
		rows = append(rows, row{name, p, s, len(kp.Lines)})
		printDetail(name, p, s)
	}

	fmt.Println("\n## Score")
	fmt.Println("| Photo | Model (read 1 / read 2) | Time 1 + 2 | Turned | Heading | Lines found | Text CER | Amounts right | Flagged | Wrong & not flagged |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|")
	for _, x := range rows {
		s, p := x.score, x.page
		fmt.Printf("| %s | %s / %s | %.1fs + %.1fs | %d° | %s %s | %d/%d | %.1f%% | %d/%d | %d (%d false alarms) | **%d** |\n",
			x.name, short(p.FirstRead.Model), short(p.SecondRead.Model),
			p.FirstRead.Elapsed.Seconds(), p.SecondRead.Elapsed.Seconds(), p.Rotation,
			okMark(s.FunctionCER < 0.25), okMark(s.DateOK),
			s.Found, x.total, 100*s.MeanCER, s.AmountsOK, x.total, s.Flagged, s.FalseAlarms, s.SilentBad)
	}
}

func printDetail(name string, p notebook.Page, s notebook.PageScore) {
	fmt.Printf("\n## %s: %q, %q, turned %d°\n", name, p.Function, p.Date, p.Rotation)
	for _, w := range p.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	if p.FirstRead.PrimaryErr != nil {
		fmt.Printf("read 1: gemini skipped: %v\n", p.FirstRead.PrimaryErr)
	}
	if p.FirstRead.Cached {
		fmt.Println("read 1: replayed from cache (no model call)")
	}
	if p.SecondRead.PrimaryErr != nil {
		fmt.Printf("read 2: gemini skipped: %v\n", p.SecondRead.PrimaryErr)
	}
	fmt.Println("| # | Read as | CER | Amount (key → read 1 / read 2) | OK | Flags |")
	fmt.Println("|---|---|---|---|---|---|")
	for _, ls := range s.Lines {
		if ls.Got == nil {
			fmt.Printf("| %s | **missed** | | %s | ❌ | |\n", ls.Key.Number, gift(ls.Key.AmountDigits, ls.Key.Gold))
			continue
		}
		g := ls.Got
		fmt.Printf("| %s | %s | %.0f%% | %s → %s / %s | %s | %s |\n", ls.Key.Number, g.Text, 100*ls.CER,
			gift(ls.Key.AmountDigits, ls.Key.Gold), gift(g.AmountDigits, g.GoldSovereigns),
			gift(g.SecondDigits, g.SecondGold), okMark(ls.AmountOK), strings.Join(g.Flags, "; "))
	}
	for _, e := range s.Extra {
		fmt.Printf("| extra %q | %s | | %s | | not in key |\n", e.Number, e.Text, gift(e.AmountDigits, e.GoldSovereigns))
	}
}

func saveCrops(dir string, p notebook.Page) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "read1.json"), p.FirstRead.JSON, 0o644); err != nil {
		return err
	}
	for i, l := range p.Lines {
		if l.Crop == nil {
			continue
		}
		b, err := notebook.EncodeJPEG(l.Crop)
		if err != nil {
			return err
		}
		n := l.Number
		if n = strings.Trim(n, ". "); n == "" {
			n = fmt.Sprintf("x%d", i+1)
		}
		if err := os.WriteFile(filepath.Join(dir, "line"+n+".jpg"), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func gift(digits string, gold float64) string {
	switch {
	case digits != "" && gold > 0:
		return fmt.Sprintf("₹%s + %g sov", digits, gold)
	case digits != "":
		return "₹" + digits
	case gold > 0:
		return fmt.Sprintf("%g sov", gold)
	}
	return "–"
}

func okMark(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

func short(model string) string {
	switch model {
	case vision.PrimaryModel:
		return "hydrogen"
	case vision.FallbackModel:
		return "haiku"
	case "":
		return "–"
	}
	return model
}
