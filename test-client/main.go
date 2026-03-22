package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	targetURL := os.Getenv("TARGET_URL")
	if targetURL == "" {
		targetURL = "http://localhost:8080/heartbeat"
	}

	interval := time.Second
	if intervalStr := os.Getenv("INTERVAL"); intervalStr != "" {
		duration, err := time.ParseDuration(intervalStr)
		if err != nil {
			log.Printf("Invalid interval format: %s, using default 1s", intervalStr)
		} else {
			interval = duration
		}
	}

	log.Printf("Starting heartbeat client. Target: %s, Interval: %s\n", targetURL, interval)

	eventTicker := time.NewTicker(interval)
	defer eventTicker.Stop()

	for range eventTicker.C {
		heartbeat(targetURL)
	}
}

func heartbeat(targetURL string) {
	start := time.Now()
	resp, err := http.Post(targetURL, "application/json", nil)
	if err != nil {
		log.Printf("Error sending heartbeat: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		duration := time.Since(start).Seconds()
		log.Printf("Heartbeat sent successfully. Status: %d, Duration: %.3fs\n", resp.StatusCode, duration)
	} else {
		log.Printf("Heartbeat failed. Status: %d\n", resp.StatusCode)
	}
}
