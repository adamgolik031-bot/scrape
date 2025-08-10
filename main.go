package main

import (
	"archive/zip"
	"bot-net-in-go/clo"
	"bot-net-in-go/ini"
	"bot-net-in-go/models"
	"bot-net-in-go/nsfw"
	"context"
	"encoding/json"
	"flag"
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
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func downloadFile(filepath string, url string) error {
	// Pobiera plik z url i zapisuje do filepath
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func unzip(src string, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}

		_, err = io.Copy(outFile, rc)

		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	//var in string
	//fmt.Printf("Do you want to use cli or browser  type Y/n is browser (if cli just enter) ")
	//fmt.Scanln(&in)
	//if in == "" || in == "n" {
	//Cli()
	//}
	//if in == "Y" {
	ini.LoadEnv()
	ini.ConnectDB()
	browser()
	// }
}

// ScrapeRequest represents the JSON payload for scrape requests
type ScrapeRequest struct {
	BaseURL    string `json:"baseURL" binding:"required"`
	Type       string `json:"type"`
	MaxVideos  string `json:"maxVideos"`
	SeeBrowser bool   `json:"seeBrowser"`
}

// ScrapeResponse represents the response structure
type ScrapeResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func browser() {
	router := gin.Default()

	// Middleware for CORS
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})
	router.Static("/static", "./static")
	router.GET("/", func(c *gin.Context) {
		c.File("./static/index.html")
	})
	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, ScrapeResponse{
			Status:  "success",
			Message: "Server is running",
		})
	})

	// Main scrape endpoint (POST)
	router.POST("/scrape", func(c *gin.Context) {
		var req ScrapeRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ScrapeResponse{
				Status:  "error",
				Message: "Invalid request format: " + err.Error(),
			})
			return
		}

		// Validate baseURL
		if req.BaseURL == "" {
			c.JSON(http.StatusBadRequest, ScrapeResponse{
				Status:  "error",
				Message: "baseURL is required",
			})
			return
		}

		// Start scraping in a goroutine to avoid timeout
		go func() {
			Scrape(req.BaseURL, req.Type, req.MaxVideos, req.SeeBrowser)
		}()

		c.JSON(http.StatusAccepted, ScrapeResponse{
			Status:  "success",
			Message: "Scraping started successfully",
			Data: map[string]interface{}{
				"baseURL":    req.BaseURL,
				"type":       req.Type,
				"maxVideos":  req.MaxVideos,
				"seeBrowser": req.SeeBrowser,
			},
		})
	})

	// GET endpoint for simple scraping (query parameters)
	router.GET("/scrape", func(c *gin.Context) {
		baseURL := c.Query("baseURL")
		if baseURL == "" {
			c.JSON(http.StatusBadRequest, ScrapeResponse{
				Status:  "error",
				Message: "baseURL parameter is required",
			})
			return
		}

		typeParam := c.Query("type")
		maxVideos := c.Query("maxVideos")
		seeBrowser, _ := strconv.ParseBool(c.Query("seeBrowser"))

		// Start scraping in a goroutine
		go func() {
			Scrape(baseURL, typeParam, maxVideos, seeBrowser)
		}()

		c.JSON(http.StatusAccepted, ScrapeResponse{
			Status:  "success",
			Message: "Scraping started successfully",
			Data: map[string]interface{}{
				"baseURL":    baseURL,
				"type":       typeParam,
				"maxVideos":  maxVideos,
				"seeBrowser": seeBrowser,
			},
		})
	})

	// Get scraping results
	router.GET("/results", func(c *gin.Context) {
		// Read results from videos.json file
		data, err := os.ReadFile("videos.json")
		if err != nil {
			c.JSON(http.StatusNotFound, ScrapeResponse{
				Status:  "error",
				Message: "No results found. Run scraping first.",
			})
			return
		}

		var videos []interface{}
		if err := json.Unmarshal(data, &videos); err != nil {
			c.JSON(http.StatusInternalServerError, ScrapeResponse{
				Status:  "error",
				Message: "Error reading results: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, ScrapeResponse{
			Status:  "success",
			Message: "Results retrieved successfully",
			Data: map[string]interface{}{
				"count":  len(videos),
				"videos": videos,
			},
		})
	})

	// Get hrefs (links found)
	router.GET("/hrefs", func(c *gin.Context) {
		data, err := os.ReadFile("hrefs.json")
		if err != nil {
			c.JSON(http.StatusNotFound, ScrapeResponse{
				Status:  "error",
				Message: "No hrefs found. Run scraping first.",
			})
			return
		}

		var hrefs []string
		if err := json.Unmarshal(data, &hrefs); err != nil {
			c.JSON(http.StatusInternalServerError, ScrapeResponse{
				Status:  "error",
				Message: "Error reading hrefs: " + err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, ScrapeResponse{
			Status:  "success",
			Message: "Hrefs retrieved successfully",
			Data: map[string]interface{}{
				"count": len(hrefs),
				"hrefs": hrefs,
			},
		})
	})

	// Get progress files
	router.GET("/progress", func(c *gin.Context) {
		files, err := os.ReadDir(".")
		if err != nil {
			c.JSON(http.StatusInternalServerError, ScrapeResponse{
				Status:  "error",
				Message: "Error reading directory: " + err.Error(),
			})
			return
		}

		var progressFiles []string
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "videos_progress_") && strings.HasSuffix(file.Name(), ".json") {
				progressFiles = append(progressFiles, file.Name())
			}
		}

		c.JSON(http.StatusOK, ScrapeResponse{
			Status:  "success",
			Message: "Progress files retrieved successfully",
			Data: map[string]interface{}{
				"count": len(progressFiles),
				"files": progressFiles,
			},
		})
	})

	// Delete results and temporary files
	router.DELETE("/cleanup", func(c *gin.Context) {
		filesToDelete := []string{"videos.json", "hrefs.json"}

		// Find and add progress files
		files, _ := os.ReadDir(".")
		for _, file := range files {
			if strings.HasPrefix(file.Name(), "videos_progress_") ||
				strings.HasSuffix(file.Name(), ".backup.") {
				filesToDelete = append(filesToDelete, file.Name())
			}
		}

		deletedFiles := []string{}
		for _, filename := range filesToDelete {
			if err := os.Remove(filename); err == nil {
				deletedFiles = append(deletedFiles, filename)
			}
		}

		c.JSON(http.StatusOK, ScrapeResponse{
			Status:  "success",
			Message: "Cleanup completed",
			Data: map[string]interface{}{
				"deletedFiles": deletedFiles,
				"count":        len(deletedFiles),
			},
		})
	})

	// Get port from environment or default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Run server
	router.Run("0.0.0.0:" + port)
}

func Cli() {
	// Definicja flag
	browserFlag := flag.String("browser", "", "Czy pokazać przeglądarkę (y/n)")
	urlFlag := flag.String("url", "", "URL video")
	typeFlag := flag.String("type", "", "Typy video (0-4, oddzielone przecinkami)")
	maxVideos := flag.String("max", "", "Maksymalna liczba video do pobrania")
	help := flag.Bool("help", false, "Pokaż pomoc")

	flag.Parse()

	// Pomoc
	if *help {
		fmt.Println("Użycie:")
		fmt.Println("  ./app [flagi]")
		fmt.Println("  ./app --browser y --url https://example.com --type 1,2 --max 10")
		fmt.Println("\nFlagi:")
		flag.PrintDefaults()
		return
	}

	// Obsługa browser
	var see_browser bool = true
	if *browserFlag != "" {
		see_browser = strings.ToLower(*browserFlag) == "y"
	} else {
		var browser string
		fmt.Print("Czy chcesz widzieć przeglądarkę? Jeśli tak wpisz Y/n, jeśli nie enter lub n: ")
		fmt.Scanln(&browser)
		if browser == "" || strings.ToLower(browser) == "n" {
			see_browser = false
		}
	}

	// Obsługa URL
	var baseURL string
	if *urlFlag != "" {
		baseURL = *urlFlag
	} else {
		fmt.Print("Podaj URL video (enter jeśli default): ")
		fmt.Scanln(&baseURL)
		if baseURL == "" {
			baseURL = "https://pl.spankbang.com/"
		}
	}

	// Wyświetlenie tabeli typów
	names := []string{"Drawing", "Hentai", "Neutral", "Porn", "Sexy"}
	fmt.Println("+---------+-------+")
	fmt.Println("| Name    | Value |")
	fmt.Println("+---------+-------+")
	for i, name := range names {
		fmt.Printf("| %-7s | %-5d |\n", name, i)
	}
	fmt.Println("+---------+-------+")

	// Obsługa typu
	var ype string
	if *typeFlag != "" {
		ype = *typeFlag
	} else {
		fmt.Println("Wybierz spośród 0-4. Kliknij jeśli chcesz więcej niż jedno, wpisz np 1,2. Wpisz enter jeśli wszystkie:")
		fmt.Scanln(&ype)
	}

	// Obsługa max videos
	var input3 string
	if *maxVideos != "" {
		input3 = *maxVideos
	} else {
		fmt.Print("Podaj max video ile chcesz pobrać (enter jeśli default): ")
		fmt.Scanln(&input3)
	}

	// Wyświetlenie wyników (dla demonstracji)
	fmt.Println("\n=== Wybrane opcje ===")
	fmt.Printf("Przeglądarka: %t\n", see_browser)
	fmt.Printf("URL: %s\n", baseURL)
	fmt.Printf("Typ: %s\n", ype)
	fmt.Printf("Max videos: %s\n", input3)
	Scrape(baseURL, ype, input3, see_browser)
}

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
		err := chromedp.Run(ctx,
			chromedp.Evaluate(`
				Array.from(document.querySelectorAll("div.main_content_container div.searches a"))
				.map(el => ({ href: el.href, name: el.textContent.trim() }))
			`, &tags),
		)
		if err != nil {
			return nil, fmt.Errorf("błąd podczas pobierania tagów: %w", err)
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

	// Zoptymalizowana funkcja przetwarzania pojedynczego video
	processVideo := func(ctx context.Context, href string, predictor *nsfw.Predictor) (*models.Video, error) {
		// Nawigacja z timeout
		navCtx, navCancel := context.WithTimeout(ctx, 15*time.Second)
		defer navCancel()

		err := chromedp.Run(navCtx,
			chromedp.Navigate(href),
			chromedp.Sleep(2*time.Second),
		)
		if err != nil {
			return nil, fmt.Errorf("błąd nawigacji: %w", err)
		}

		// Pobierz URL obrazka
		var imgURL string
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

		// Pobierz i przetwórz obrazek
		filename, err := getFilenameFromURL(imgURL)
		if err != nil {
			filename = "temp_image.jpg"
		}

		// Dodaj timestamp do nazwy pliku aby uniknąć konfliktów
		filename = fmt.Sprintf("%d_%s", time.Now().UnixNano(), filename)

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

		// Pobierz szczegóły video (równolegle)
		var (
			title    string
			videoSrc string
			tags     []models.Tags
			wg       sync.WaitGroup
			mu       sync.Mutex
			errors   []error
		)

		wg.Add(3)

		// Pobierz tytuł
		go func() {
			defer wg.Done()
			err := chromedp.Run(ctx,
				chromedp.Text("div.main_content_container h1.main_content_title", &title, chromedp.NodeVisible),
			)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("błąd pobierania tytułu: %w", err))
				mu.Unlock()
			}
		}()

		// Pobierz źródło wideo
		go func() {
			defer wg.Done()
			err := chromedp.Run(ctx,
				chromedp.Evaluate(`
				(function() {
					var video = document.querySelector("div.main_content_container div#video div#video_container div#main_video_player source");
					return video ? video.src : null;
				})()
			`, &videoSrc),
			)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("błąd pobierania src wideo: %w", err))
				mu.Unlock()
			}
		}()

		// Pobierz tagi
		go func() {
			defer wg.Done()
			var err error
			tags, err = getTags(ctx)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("błąd pobierania tagów: %w", err))
				mu.Unlock()
				tags = []models.Tags{}
			}
		}()

		wg.Wait()

		// Sprawdź czy były błędy krytyczne
		if len(errors) > 2 {
			return nil, fmt.Errorf("za dużo błędów: %v", errors)
		}

		cleanURL := strings.ReplaceAll(videoSrc, "\\u0026", "&")

		video := &models.Video{
			Title:      title,
			Tags:       tags,
			Href:       cleanURL,
			Link:       href,
			Prediction: models.Prediction(result),
			Img:        imgURL,
		}

		return video, nil
	}

	// Funkcja zapisu wyników z backup
	saveResults := func(videos []models.Video, filename string) error {
		// Backup poprzedniej wersji jeśli istnieje
		if _, err := os.Stat(filename); err == nil {
			backupName := fmt.Sprintf("%s.backup.%d", filename, time.Now().Unix())
			os.Rename(filename, backupName)
		}

		file, err := os.Create(filename)
		if err != nil {
			return fmt.Errorf("błąd podczas tworzenia pliku: %w", err)
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")

		if err := encoder.Encode(videos); err != nil {
			return fmt.Errorf("błąd podczas serializacji do JSON: %w", err)
		}
		return nil
	}

	// GŁÓWNA LOGIKA PRZETWARZANIA
	//	// Gdzie pobierzemy i rozpakujemy Chromium
	dir := "./chromium"
	os.MkdirAll(dir, 0755)

	zipPath := filepath.Join(dir, "chromium.zip")

	// Link do Chromium portable dla Linux x64 (przykład)
	// Możesz podmienić na inny link z chromium w wersji portable
	url := "https://commondatastorage.googleapis.com/chromium-browser-snapshots/Linux_x64/1056759/chrome-linux.zip"

	fmt.Println("Pobieram Chromium...")
	err := downloadFile(zipPath, url)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Rozpakowuję Chromium...")
	err = unzip(zipPath, dir)
	if err != nil {
		log.Fatal(err)
	}
	execPath := filepath.Join(dir, "chrome-linux", "chrome")
	fmt.Println("Sprawdzam wersję Chromium:")
	out, err := exec.Command(execPath, "--version").Output()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(out))

	// Zwiększony timeout dla całej operacji
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
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

	// Pobierz linki
	var hrefs []string
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
	maxChecked := limit // Sprawdź 3x więcej niż potrzebujesz
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
			// Nie loguj każdego błędu, tylko co 10-ty
			if i%10 == 0 {
				log.Printf("Video %d nie przeszło walidacji: %v", i+1, err)
			}
			i++
			continue
		}

		video.Id = uint(processedCount)
		videos = append(videos, *video)
		processedCount++
		i++

		// Zapisuj progress co 10 videosów
		if processedCount%10 == 0 {
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
