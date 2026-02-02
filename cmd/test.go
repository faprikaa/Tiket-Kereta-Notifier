package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func main() {

	url := "https://sc-microservice-tiketkai.bmsecure.id/train/search"
	method := "POST"

	payload := strings.NewReader(`7ftMyJGeEJP3i3D0DWRlVo71zWwFZFbYNO1CvShjrivhKfGfP3i7mHnBOFLpnZDzHHQtP6Kyxuy1/fVAQ0nWhDVHswpQBsVum1FgppjGpnP86AESxj/gTdSux6G0w8G96/KfO9ZGfZ7QPk0v6hD4CpEbKAwJ6nTh/MAISSg5yCr2eEI3bDz+ZjhKLO6m5BNRJdEm7zDeRptV8aNwCYSGQHDw3SM6FOimbKMLCG0j7+fOB/jiAWo07XljhCIajMBmmZB28L96vIeXoCHrWZpKh5/O4xBuFDTt40Vh6+/189fc+7PFyB+P+0Nb62a412JBCPeccJbAfV+j+7pmYD5yuVadOcHDvA4HWKgE5u87TCh+jlGGtycksAIXZAMSKCb3VTH6iY1VkXbgSD061iVI1S484l22uFXqj2UEDYIwm/dkPmOxIxOw7choDQcQlQZhFRwUNFcH5/4zBvNT7TAO/ghgh4hNmpYPS1l5xm0eLAibrYddkZXvaJKYXs8RE3pilS9ODz2OHFfQW9n4aLNu1lXnfKpi4U66Nh4hRMAHGCy9v1vf+YHG+BGQUoLxNMbqOzDvpBf0WkxsPg9iLvf3AQKkDScVA7sp+lEFGbNrhTWzsrX893SkBMKx2dJPv7O405SUAlRQ05QuIyJ4PFWRqCB/x3Cq8acztz2DHF0k7r7fJwTfwwQTV/rhXL7iDC1aFBSke7+5F5678fId2v+3ax+7h1LAGkGuLPA3kJXtQLOp9LSAnI84IAb870Gxnmr9Yw4p+hLLuHRL+PikX48a4t3EY6J8UCXqyi88xNLn/1aR8Mbmp+5DuvYoZ8EdUKH1PqCPexvP5S9iOuu3rQrW56/g7A1otZK8qZ2mIABQJRuFjYjfoqxg7oaYdvc1XW4HXJJ9xpJjSE4p50wbdhzN78081DCqfoHiJSuX2zF8QS5mJyZIazxC8i1f/PVxl6LHZJgx3sQgizMmOsktEWTsWbkTWNrHBuPobWxljqVDdY8XQ1dRHSj+wL6eU9EGzKTHmMmytiIwsTwSCzILeim6Pg==`)

	client := &http.Client{}
	req, err := http.NewRequest(method, url, payload)

	if err != nil {
		fmt.Println(err)
		return
	}
	req.Header.Add("accept", "application/json, text/plain, */*")
	req.Header.Add("accept-language", "en-US,en;q=0.9")
	req.Header.Add("content-type", "text/plain")
	req.Header.Add("origin", "https://m.tiketkai.com")
	req.Header.Add("priority", "u=1, i")
	req.Header.Add("referer", "https://m.tiketkai.com/")
	req.Header.Add("sec-ch-ua", "\"Not(A:Brand\";v=\"8\", \"Chromium\";v=\"144\", \"Microsoft Edge\";v=\"144\"")
	req.Header.Add("sec-ch-ua-mobile", "?0")
	req.Header.Add("sec-ch-ua-platform", "\"Windows\"")
	req.Header.Add("sec-fetch-dest", "empty")
	req.Header.Add("sec-fetch-mode", "cors")
	req.Header.Add("sec-fetch-site", "cross-site")
	req.Header.Add("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36 Edg/144.0.0.0")

	res, err := client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(body))
}
