// Package traveloka provides train search functionality using Traveloka API
package traveloka

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// APIUrl is the Traveloka train search API endpoint
const APIUrl = "https://www.traveloka.com/api/v2/train/search/inventoryv2"

// Search performs a train search using Traveloka API
func Search(origin, destination string, day, month, year int) {
	client := &http.Client{}

	payload := fmt.Sprintf(`{"fields":[],"data":{"departureDate":{"day":%d,"month":%d,"year":%d},"returnDate":null,"destination":"%s","origin":"%s","numOfAdult":1,"numOfInfant":0,"providerType":"KAI","currency":"IDR","trackingMap":{"utmId":null,"utmEntryTimeMillis":0}},"clientInterface":"desktop"}`,
		day, month, year, destination, origin)

	data := strings.NewReader(payload)
	req, err := http.NewRequest("POST", APIUrl, data)
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("origin", "https://www.traveloka.com")
	req.Header.Set("priority", "u=1, i")
	req.Header.Set("referer", fmt.Sprintf("https://www.traveloka.com/id-id/kereta-api/search?st=%s.%s&dt=%d-%d-%d.null&ps=1.0&pd=KAI", origin, destination, day, month, year))
	req.Header.Set("sec-ch-ua", `"Not(A:Brand";v="8", "Chromium";v="144", "Microsoft Edge";v="144"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.0.0")
	req.Header.Set("x-client-interface", "desktop")
	req.Header.Set("x-domain", "train")
	req.Header.Set("x-route-prefix", "id-id")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", bodyText)
}
