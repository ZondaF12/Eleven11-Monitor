package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/proxy"
	"github.com/joho/godotenv"
)

// Discord webhook configuration
type DiscordWebhook struct {
	Content string `json:"content"`
}

// Configuration struct to hold Discord settings
type Config struct {
	WebhookURL string
	UserID     string
}

func formatProxy(proxyStr string) string {
	// Expected format: ip:port:user:pass
	// Convert to: http://user:pass@ip:port
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return ""
	}

	parts := strings.Split(proxyStr, ":")
	if len(parts) != 4 {
		return ""
	}

	ip := parts[0]
	port := parts[1]
	user := parts[2]
	pass := parts[3]

	return fmt.Sprintf("http://%s:%s@%s:%s", user, pass, ip, port)
}

func readProxiesFromFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening proxy file: %v", err)
	}
	defer file.Close()

	var proxies []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		proxy := formatProxy(scanner.Text())
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading proxy file: %v", err)
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no valid proxies found in file")
	}

	return proxies, nil
}

func sendDiscordNotification(config Config, message string) error {
	webhook := DiscordWebhook{
		Content: fmt.Sprintf("<@%s> %s", config.UserID, message),
	}

	jsonData, err := json.Marshal(webhook)
	if err != nil {
		return fmt.Errorf("error marshaling JSON: %v", err)
	}

	resp, err := http.Post(config.WebhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error sending Discord notification: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 204 {
		return fmt.Errorf("unexpected status code from Discord: %d", resp.StatusCode)
	}

	return nil
}

func main() {
	// Load .env file
	if os.Getenv("ENV") != "production" {
		if err := godotenv.Load(); err != nil {
			fmt.Printf("Error loading .env file: %v\n", err)
			return
		}
	}

	// Discord configuration
	config := Config{
		WebhookURL: os.Getenv("DISCORD_WEBHOOK_URL"),
		UserID:     os.Getenv("DISCORD_USER_ID"),
	}

	if config.WebhookURL == "" || config.UserID == "" {
		fmt.Println("Please set DISCORD_WEBHOOK_URL and DISCORD_USER_ID environment variables in your .env file")
		return
	}

	// Read proxies from file
	proxyURLs, err := readProxiesFromFile("proxies.txt")
	if err != nil {
		fmt.Printf("Error reading proxies: %v\n", err)
		return
	}

	fmt.Printf("Loaded %d proxies\n", len(proxyURLs))

	// Create proxy switcher
	rp, err := proxy.RoundRobinProxySwitcher(proxyURLs...)
	if err != nil {
		fmt.Printf("Error creating proxy switcher: %v\n", err)
		return
	}

	// Configure collector
	c := colly.NewCollector(
		colly.AllowURLRevisit(),
	)

	// Set proxy switcher
	c.SetProxyFunc(rp)

	// Channel to communicate the result
	resultChan := make(chan bool, 1)

	var lastState bool = true // true means out of stock
	var firstCheck bool = true

	// Look for meta tags with og:availability property
	c.OnHTML("meta[property='og:availability']", func(e *colly.HTMLElement) {
		availability := e.Attr("content")
		timestamp := time.Now().Format("2006-01-02 15:04:05")

		// Check if out of stock
		isOutOfStock := strings.ToLower(availability) == "out of stock"

		if isOutOfStock {
			fmt.Printf("[%s] OUT OF STOCK FOUND! Availability: %s\n", timestamp, availability)
			resultChan <- true
		} else {
			fmt.Printf("[%s] In stock. Availability: %s\n", timestamp, availability)
			// Send Discord notification if state changed from out of stock to in stock
			if lastState && !firstCheck {
				err := sendDiscordNotification(config, "🚨 Item is now IN STOCK! 🚨\nhttps://www.eleven11prints.com/product-page/the-eleven-11-4-watch-ruck-case/")
				if err != nil {
					fmt.Printf("Error sending Discord notification: %v\n", err)
				}
			}
			resultChan <- false
		}
	})

	// Error handling
	c.OnError(func(r *colly.Response, err error) {
		timestamp := time.Now().Format("2006-01-02 15:04:05")
		fmt.Printf("[%s] Request URL: %v failed with response: %v\nError: %v\n",
			timestamp, r.Request.URL, r, err)
	})

	// Target URL
	cacheBuster := time.Now()
	url := fmt.Sprintf("https://www.eleven11prints.com/product-page/the-eleven-11-4-watch-ruck-case/?limit=%d", cacheBuster.Unix())

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// First check
	err = c.Visit(url)
	if err != nil {
		fmt.Printf("Error visiting site: %v\n", err)
		return
	}

	// Wait for first result
	lastState = <-resultChan
	firstCheck = false

	// Continue checking
	for range ticker.C {
		err = c.Visit(url)
		if err != nil {
			fmt.Printf("Error visiting site: %v\n", err)
			continue
		}

		// Wait for result and update state
		lastState = <-resultChan
	}
}
