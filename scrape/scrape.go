package scrape

import (
	"bot-net-in-go/clo"
	"bot-net-in-go/models"
	"bot-net-in-go/nsfw"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/sirupsen/logrus"
)

func Scrape(baseURL string, ype string, input3 string, seeBrowser bool) {
	var good_array []int

	if ype == "" {
		good_array = []int{0, 1, 2, 3, 4}
	} else {
		parts := strings.Split(ype, ",")
		for _, part := range parts {
			num, err := strconv.Atoi(strings.TrimSpace(part))
			if err == nil {
				good_array = append(good_array, num)
			}
		}
	}

	var nr_video int
	if input3 == "" {
		nr_video = 0
	} else {
		var err error
		nr_video, err = strconv.Atoi(input3)
		if err != nil {
			fmt.Println("Podaj poprawną liczbę!")
			return
		}
	}

	if nr_video != 0 {
		fmt.Println("Pobierasz max", nr_video, "wideo")
	} else {
		fmt.Println("Pobierasz pełną listę wideo")
	}

	// FUNKCJE WEWNĘTRZNE

	// Funkcja pobierania tagów
	getTags := func(ctx context.Context) ([]models.Tags, error) {
		var tags []models.Tags
		if strings.Contains(baseURL, "spangbang") {
			err := chromedp.Run(ctx,
				chromedp.Evaluate(`
				Array.from(document.querySelectorAll("div.main_content_container div.searches a"))
					.map(el => ({ href: el.href, name: el.textContent.trim() }))
			`, &tags),
			)
			if err != nil {
				return nil, fmt.Errorf("błąd podczas pobierania tagów: %w", err)
			}
		}

		if strings.Contains(baseURL, "freshporno") {
			err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				exists, err := clo.ElementExists(ctx, `div.open-desc-and-tags span.plus`)
				if err != nil {
					return err
				}

				if exists {
					log.Println("Znaleziono button potwierdzenia wieku, klikam...")
					if err := chromedp.Click(`div.open-desc-and-tags span.plus`).Do(ctx); err != nil {
						return fmt.Errorf("błąd podczas kliknięcia przycisku: %w", err)
					}

					log.Println("Czekam na pojawienie się tagów...")
					ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()

					if err := chromedp.Run(ctxTimeout, chromedp.WaitVisible(`ul.video-tags li a`, chromedp.ByQuery)); err != nil {
						return fmt.Errorf("błąd czekania na tagi: %w", err)
					}
				}

				log.Println("Pobieram tagi...")
				err = chromedp.Run(ctx,
					chromedp.Evaluate(`
            Array.from(document.querySelectorAll("ul.video-tags li a")).map(el => {
                return {
                    href: el.href,
                    name: el.textContent.trim()
                };
            })
        `, &tags),
				)
				if err != nil {
					return fmt.Errorf("błąd podczas pobierania tagów: %w", err)
				}

				if len(tags) == 0 {
					log.Println("Nie znaleziono tagów.")
				} else {
					log.Printf("Znaleziono %d tagów.\n", len(tags))
				}

				return nil
			}))
			if err != nil {
				return nil, fmt.Errorf("błąd podczas przetwarzania tagów: %w", err)
			}
		}

		return tags, nil
	}

	// Funkcja konwersji WebP do JPEG
	convertWebPToJPEG := func(inputPath string) (string, error) {
		cmd := exec.Command("file", inputPath)
		output, err := cmd.Output()
		if err != nil {
			return inputPath, err
		}

		if strings.Contains(string(output), "Web/P") {
			outputPath := strings.Replace(inputPath, ".jpg", "_converted.jpg", 1)
			convertCmd := exec.Command("dwebp", inputPath, "-o", outputPath)
			err = convertCmd.Run()
			if err != nil {
				return inputPath, fmt.Errorf("błąd konwersji WebP: %w", err)
			}
			os.Remove(inputPath)
			return outputPath, nil
		}
		return inputPath, nil
	}

	// Funkcja wyciągnięcia nazwy pliku z URL
	getFilenameFromURL := func(imgURL string) (string, error) {
		parsedURL, err := url.Parse(imgURL)
		if err != nil {
			return "", err
		}

		filename := filepath.Base(parsedURL.Path)
		if filename == "" || filename == "." || !strings.Contains(filename, ".") {
			filename = "downloaded_image.jpg"
		}
		return filename, nil
	}

	// Zoptymalizowana funkcja pobierania obrazka z retry i timeout
	downloadImageWithRetry := func(imgURL, savePath string, maxRetries int) error {
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		for attempt := 1; attempt <= maxRetries; attempt++ {
			response, err := client.Get(imgURL)
			if err != nil {
				if attempt == maxRetries {
					return fmt.Errorf("błąd podczas pobierania obrazka po %d próbach: %w", maxRetries, err)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				if attempt == maxRetries {
					return fmt.Errorf("błąd HTTP: %d po %d próbach", response.StatusCode, maxRetries)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}

			file, err := os.Create(savePath)
			if err != nil {
				return fmt.Errorf("błąd tworzenia pliku: %w", err)
			}
			defer file.Close()

			_, err = io.Copy(file, response.Body)
			if err != nil {
				return fmt.Errorf("błąd zapisywania pliku: %w", err)
			}
			return nil
		}
		return fmt.Errorf("osiągnięto maksymalną liczbę prób: %d", maxRetries)
	}

	// FIXED: Zoptymalizowana funkcja przetwarzania pojedynczego video
	processVideo := func(ctx context.Context, href string, predictor *nsfw.Predictor) (*models.Video, error) {
		log.Printf("Rozpoczynam przetwarzanie: %s", href)

		// Nawigacja z timeout
		navCtx, navCancel := context.WithTimeout(ctx, 15*time.Second) // Zwiększony timeout
		defer navCancel()

		err := chromedp.Run(navCtx,
			chromedp.Navigate(href),
			chromedp.Sleep(3*time.Second), // Więcej czasu na ładowanie
		)
		if err != nil {
			return nil, fmt.Errorf("błąd nawigacji: %w", err)
		}

		log.Println("Nawigacja zakończona, pobieranie URL obrazka...")

		// Pobierz URL obrazka
		var imgURL string
		if strings.Contains(baseURL, "spangbang") {
			err = chromedp.Run(ctx,
				chromedp.AttributeValue(
					`div.main_content_container div.play_cover img#player_cover_img`,
					"src",
					&imgURL,
					nil,
				),
			)
			if err != nil {
				return nil, fmt.Errorf("błąd pobierania URL obrazka: %w", err)
			}
		}
		if strings.Contains(baseURL, "freshporno") {
			err = chromedp.Run(ctx,
				chromedp.AttributeValue(
					`div.fp-poster img`,
					"src",
					&imgURL,
					nil,
				),
			)
			if err != nil {
				return nil, fmt.Errorf("błąd pobierania URL obrazka: %w", err)
			}
		}

		log.Printf("URL obrazka pobrany: %s", imgURL)

		// Pobierz i przetwórz obrazek
		filename, err := getFilenameFromURL(imgURL)
		if err != nil {
			filename = "temp_image.jpg"
		}

		// Dodaj timestamp do nazwy pliku aby uniknąć konfliktów
		filename = fmt.Sprintf("%d_%s", time.Now().UnixNano(), filename)

		log.Printf("Pobieranie obrazka do: %s", filename)

		err = downloadImageWithRetry(imgURL, filename, 3)
		if err != nil {
			return nil, fmt.Errorf("błąd pobierania obrazka: %w", err)
		}

		// Konwersja jeśli potrzeba
		convertedPath, err := convertWebPToJPEG(filename)
		if err != nil {
			os.Remove(filename)
			return nil, fmt.Errorf("błąd konwersji: %w", err)
		}

		log.Println("Predykcja NSFW...")

		// Predykcja NSFW
		image := predictor.NewImage(convertedPath, 3)
		result := predictor.Predict(image)
		category := nsfw.GetMaxCategory(result)

		// Usuń plik tymczasowy
		os.Remove(convertedPath)

		// Sprawdź czy kategoria jest akceptowalna
		if !slices.Contains(good_array, category) {
			return nil, fmt.Errorf("kategoria %d nie jest akceptowalna", category)
		}

		log.Printf("Kategoria akceptowalna: %d, pobieranie szczegółów...", category)

		// FIXED: Pobierz szczegóły video sekwencyjnie zamiast równolegle
		var (
			title    string
			videoSrc string
			tags     []models.Tags
		)

		// Pobierz tytuł
		log.Println("Pobieranie tytułu...")
		if strings.Contains(baseURL, "spangbang") {
			err := chromedp.Run(ctx,
				chromedp.Text("div.main_content_container h1.main_content_title", &title, chromedp.NodeVisible),
			)
			if err != nil {
				log.Printf("Błąd pobierania tytułu: %v", err)
				title = "Unknown Title"
			}
		}
		if strings.Contains(baseURL, "freshporno") {
			err := chromedp.Run(ctx,
				chromedp.Text("div.video-info div.title-holder h1", &title, chromedp.NodeVisible),
			)
			if err != nil {
				log.Printf("Błąd pobierania tytułu: %v", err)
				title = "Unknown Title"
			}
		}
		log.Printf("Tytuł: %s", title)

		// Pobierz źródło wideo
		log.Println("Pobieranie źródła wideo...")
		if strings.Contains(baseURL, "spangbang") {
			err := chromedp.Run(ctx,
				chromedp.Evaluate(`
				(function() {
					var video = document.querySelector("div.main_content_container div#video div#video_container div#main_video_player source");
					return video ? video.src : null;
				})()
			`, &videoSrc),
			)
			if err != nil {
				log.Printf("Błąd pobierania src wideo: %v", err)
			}
		}
		if strings.Contains(baseURL, "freshporno") {
			// FIXED: Dodaj timeout i lepsze oczekiwanie
			videoCtx, videoCancel := context.WithTimeout(ctx, 30*time.Second)
			defer videoCancel()

			err := chromedp.Run(videoCtx,
				// czekaj, aż element video będzie gotowy / widoczny
				chromedp.WaitReady(`video[class=fp-engine]`, chromedp.ByQuery),
				chromedp.Sleep(2*time.Second), // Dodatkowe oczekiwanie

				// teraz pobierz src
				chromedp.Evaluate(`
            (function() {
                var video = document.querySelector("video[class=fp-engine]");
                console.log("Found video element:", video);
                var src = video ? video.src : null;
                console.log("Video src:", src);
                return src;
            })()
        `, &videoSrc),
			)
			if err != nil {
				log.Printf("Błąd pobierania src wideo: %v", err)
				// Nie zwracaj błędu, kontynuuj bez video src
			}
		}
		log.Printf("Źródło wideo: %s", videoSrc)

		// Pobierz tagi
		log.Println("Pobieranie tagów...")
		var err2 error
		tags, err2 = getTags(ctx)
		if err2 != nil {
			log.Printf("Błąd pobierania tagów: %v", err2)
			tags = []models.Tags{} // Kontynuuj bez tagów
		}
		log.Printf("Pobrano %d tagów", len(tags))

		cleanURL := strings.ReplaceAll(videoSrc, "\\u0026", "&")

		video := &models.Video{
			Title:      title,
			Tags:       tags,
			Href:       cleanURL,
			Link:       href,
			Prediction: models.Prediction(result),
			Img:        imgURL,
		}

		log.Printf("Video przetworzony pomyślnie: %s", title)

		return video, nil
	}

	saveResults := func(videos []models.Video, filename string) error {
		// Tworzenie katalogów jeśli nie istnieją
		if err := os.MkdirAll("tmp/json", 0755); err != nil {
			return fmt.Errorf("błąd tworzenia katalogu tmp/json: %w", err)
		}
		if err := os.MkdirAll("tmp/backup", 0755); err != nil {
			return fmt.Errorf("błąd tworzenia katalogu tmp/backup: %w", err)
		}

		// Backup poprzedniej wersji jeśli istnieje
		if _, err := os.Stat(filename); err == nil {
			backupName := fmt.Sprintf("tmp/backup/%s.backup.%d.json",
				filepath.Base(filename),
				time.Now().Unix(),
			)
			if err := os.Rename(filename, backupName); err != nil {
				return fmt.Errorf("błąd przenoszenia do backup: %w", err)
			}
		}

		// Zapis głównego pliku
		if err := writeJSON(filename, videos); err != nil {
			return err
		}

		// Zapis do tmp/json/
		tmpJSONFile := filepath.Join("tmp/json", filepath.Base(filename))
		if err := writeJSON(tmpJSONFile, videos); err != nil {
			return err
		}

		return nil
	}

	// GŁÓWNA LOGIKA PRZETWARZANIA

	// Zwiększony timeout dla całej operacji
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second) // Zwiększony timeout
	defer cancel()

	// Zoptymalizowane opcje Chrome
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !seeBrowser),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("exclude-switches", "enable-automation"),
		chromedp.Flag("disable-extensions", false),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		chromedp.Flag("disable-web-security", true),
		chromedp.Flag("disable-features", "VizDisplayCompositor"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer cancelCtx()

	// Inicjalizuj predictor raz na początku
	logrus.SetLevel(logrus.InfoLevel)
	predictor, err := nsfw.NewLatestPredictor()
	if err != nil {
		log.Fatalf("błąd tworzenia predictora: %v", err)
	}

	// Nawiguj do strony głównej
	err = chromedp.Run(ctx,
		chromedp.Navigate(baseURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return clo.WaitForCloudflareBypass(ctx)
		}),
		chromedp.Sleep(1*time.Second),
	)
	if err != nil {
		log.Fatal("Błąd podczas nawigacji lub przejścia przez Cloudflare:", err)
	}

	// Obsługa potwierdzenia wieku
	if strings.Contains(baseURL, "spangbang") {
		err = chromedp.Run(ctx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				exists, err := clo.ElementExists(ctx, `div#age-check-content button`)
				if err != nil {
					return err
				}
				if exists {
					log.Println("Znaleziono button potwierdzenia wieku, klikam...")
					return chromedp.Click(`div#age-check-content button`).Do(ctx)
				}
				return nil
			}),
			chromedp.Sleep(2*time.Second),
		)
		if err != nil {
			log.Fatal("Błąd podczas obsługi potwierdzenia wieku:", err)
		}
	}

	// Pobierz linki
	var hrefs []string
	if strings.Contains(baseURL, "spangbang") {
		err = chromedp.Run(ctx,
			chromedp.Evaluate(`
		const links = Array.from(document.querySelectorAll("main div[data-testid='video-item'] a"))
			.filter(el => el.href && el.href.includes('/video/') && !el.href.includes('/channel/'))
			.map(el => el.href);
		[...new Set(links)]
	`, &hrefs),
		)
		if err != nil {
			log.Fatal("Błąd podczas pobierania href:", err)
		}
	}
	if strings.Contains(baseURL, "freshporno") {
		err = chromedp.Run(ctx,
			chromedp.Evaluate(`
				const links = Array.from(document.querySelectorAll("div.thumbs-inner > a"))
					.filter(el => el.href && el.href.includes('/videos/'))
					.map(el => el.href);
				[...new Set(links)]
			`, &hrefs),
		)
		if err != nil {
			log.Fatal("Błąd podczas pobierania href:", err)
		}
	}

	// Zapisz linki do pliku
	jsonData, _ := json.MarshalIndent(hrefs, "", "  ")
	os.WriteFile("hrefs.json", jsonData, 0644)

	fmt.Printf("Znaleziono %d elementów video\n", len(hrefs))

	// Ustal limit
	limit := len(hrefs)
	if nr_video > 0 && nr_video < len(hrefs) {
		limit = nr_video
	}

	fmt.Printf("Przetwarzanie maksymalnie %d wideo...\n", limit)

	videos := make([]models.Video, 0, limit)
	processedCount := 0
	i := 0
	maxChecked := limit // Sprawdź dokładnie tyle ile potrzebujesz
	if maxChecked > len(hrefs) {
		maxChecked = len(hrefs)
	}

	// Główna pętla przetwarzania z progress tracking
	for processedCount < limit && i < maxChecked {
		href := hrefs[i]

		// Progress indicator
		progress := float64(i) / float64(maxChecked) * 100
		fmt.Printf("\rPostęp: %.1f%% | Sprawdzono: %d/%d | Zaakceptowano: %d/%d",
			progress, i+1, maxChecked, processedCount, limit)

		video, err := processVideo(ctx, href, predictor)
		if err != nil {
			log.Printf("Video %d nie przeszło walidacji: %v", i+1, err)
			i++
			continue
		}

		video.Id = uint(processedCount)
		videos = append(videos, *video)
		processedCount++
		i++

		// Zapisuj progress co 1 video dla testów
		if processedCount%1 == 0 {
			saveResults(videos, fmt.Sprintf("videos_progress_%d.json", processedCount))
		}
	}

	fmt.Printf("\n") // Nowa linia po progress indicator

	// Podsumowanie wyników
	if processedCount == 0 {
		fmt.Printf("\n❌ Nie znaleziono żadnych filmów pasujących do wybranych kategorii!\n")
		fmt.Printf("Sprawdzono %d/%d filmów.\n", i, len(hrefs))
	} else if processedCount < limit {
		fmt.Printf("\n⚠️ Znaleziono tylko %d z %d żądanych filmów.\n", processedCount, limit)
		fmt.Printf("Sprawdzono %d filmów z dostępnych %d.\n", i, len(hrefs))
	} else {
		fmt.Printf("\n✅ Pomyślnie znaleziono wszystkie %d filmów!\n", processedCount)
	}

	// Finalne zapisanie wyników
	if len(videos) > 0 {
		if err := saveResults(videos, "videos.json"); err != nil {
			log.Fatalf("Błąd zapisywania końcowych wyników: %v", err)
		}
		fmt.Printf("Zapisano %d wideo do videos.json\n", len(videos))
	}
}

// Pomocnicza funkcja do zapisu JSON
func writeJSON(path string, data interface{}) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("błąd podczas tworzenia pliku %s: %w", path, err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("błąd podczas serializacji do JSON (%s): %w", path, err)
	}
	return nil
}
