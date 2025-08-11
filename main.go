package main

import (
	"bot-net-in-go/ini"
	"bot-net-in-go/scrape"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func main() {
	//var in string
	//fmt.Printf("Do you want to use cli or browser  type Y/n is browser (if cli just enter) ")
	//fmt.Scanln(&in)
	//if in == "" || in == "n" {
	//	Cli()
	//}
	//if in == "Y" {
	ini.LoadEnv()
	ini.CreateDatabase()
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
	Predition  bool   `json:"predition"`
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
			scrape.Scrape(req.BaseURL, req.Type, req.MaxVideos, req.SeeBrowser, req.Predition)
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
	port := os.Getenv("port_server")
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
	predit := flag.String("predition", "", "predition dla vidoe")

	help := flag.Bool("help", false, "Pokaż pomoc")

	flag.Parse()

	// Pomoc
	if *help {
		fmt.Println("Użycie:")
		fmt.Println("  ./app [flagi]")
		fmt.Println("  ./app --browser y --url https://example.com --type 1,2 --max 10 --predit false")
		fmt.Println("\nFlagi:")
		flag.PrintDefaults()
		return
	}
	var predition bool = false
	if *predit != "false" {
		predition = true
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
	scrape.Scrape(baseURL, ype, input3, see_browser, predition)
}
