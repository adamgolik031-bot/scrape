package clo

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/chromedp/chromedp"
)

// elementExists sprawdza czy element istnieje na stronie
func ElementExists(ctx context.Context, selector string) (bool, error) {
	var exists bool
	err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector('%s') !== null`, selector), &exists),
	)
	return exists, err
}

// waitForCloudflareBypass czeka na przejście przez ochronę Cloudflare
func WaitForCloudflareBypass(ctx context.Context) error {
	log.Println("Oczekiwanie na przejście przez ochronę Cloudflare...")

	// Czekaj maksymalnie 30 sekund na przejście przez Cloudflare
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout podczas oczekiwania na przejście przez Cloudflare")
		default:
			// Sprawdź czy jesteśmy nadal na stronie Cloudflare
			var title string
			err := chromedp.Run(ctx, chromedp.Title(&title))
			if err == nil && title != "Just a moment..." {
				log.Println("Pomyślnie przeszedłem przez ochronę Cloudflare")
				return nil
			}

			// Sprawdź czy istnieje charakterystyczny element Cloudflare
			cloudflareExists, err := ElementExists(ctx, "#cf-wrapper")
			if err == nil && !cloudflareExists {
				// Sprawdź czy strona jest już załadowana (szukaj elementów docelowych)
				pageLoaded, err := ElementExists(ctx, "body")
				if err == nil && pageLoaded {
					log.Println("Strona została załadowana")
					return nil
				}
			}

			time.Sleep(1 * time.Second)
		}
	}
}
