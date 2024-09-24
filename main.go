package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"io"
	"math/rand"
	"os"
	"bufio"
)

type Result struct {
	URL           string
	Header        string
	StatusCode    int
	ContentLength int64
}

func main() {
	urlPtr := flag.String("url", "", "URL to make requests to")
	headersFilePtr := flag.String("headers", "", "File containing headers for requests")
	proxyPtr := flag.String("proxy", "", "Proxy server IP:PORT (e.g., 127.0.0.1:8080)")
	delayPtr := flag.Int("delay", 0, "Delay in seconds between requests")  // New delay flag
	foundOnlyPtr := flag.Bool("found", false, "Print only headers with status code 200")
	noConcurrentPtr := flag.Bool("no-concurrent", false, "Disable concurrent requests, send one request at a time") // Correctly added the flag here
	quietPtr := flag.Bool("q", false, "Suppress banner")
	flag.Parse()
	log.SetFlags(0)
	
	// Print tool banner
	if !*quietPtr {
		log.Print(`


	   __               __                      
	  / /  ___ ___  ___/ /__ _______ _    _____ 
	 / _ \/ -_) _ \/ _  / -_) __/ _ \ |/|/ / _ \
	/_//_/\__/\_,_/\_,_/\__/_/ .__/__,__/_//_/
	                          /_/               
    
`)
	}

	if *urlPtr == "" {
		fmt.Println("Please provide a valid URL using the -url flag")
		return
	}

	if *headersFilePtr == "" {
		fmt.Println("Please provide a valid headers file using the -headers flag")
		return
	}

	headers, err := readHeadersFromFile(*headersFilePtr)
	if err != nil {
		fmt.Println("Error reading headers:", err)
		return
	}

	var wg sync.WaitGroup
	results := make(chan Result)

	if *noConcurrentPtr {
		// Sequential requests (one at a time)
		for _, header := range headers {
			wg.Add(1)
			go func(header string) {
				defer wg.Done()

				response, err := makeRequest(*urlPtr, header, *proxyPtr, *delayPtr)
				if err != nil {
					return
				}

				result := Result{
					URL:           *urlPtr + "?cachebuster=" + generateCacheBuster(),
					Header:        header,
					StatusCode:    response.StatusCode,
					ContentLength: response.ContentLength,
				}
				results <- result
			}(header)
			
			// Wait for this request to finish before sending the next one
			wg.Wait()
		}
	} else {
		// Concurrent requests (default behavior)
		for _, header := range headers {
			wg.Add(1)
			go func(header string) {
				defer wg.Done()

				response, err := makeRequest(*urlPtr, header, *proxyPtr, *delayPtr)
				if err != nil {
					return
				}

				result := Result{
					URL:           *urlPtr + "?cachebuster=" + generateCacheBuster(),
					Header:        header,
					StatusCode:    response.StatusCode,
					ContentLength: response.ContentLength,
				}
				results <- result
			}(header)
		}

		// Close the results channel after all requests are done
		go func() {
			wg.Wait()
			close(results)
		}()
	}

	printResults(results, *foundOnlyPtr)
}

func readHeadersFromFile(filename string) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	headers := make([]string, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		headers = append(headers, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return headers, nil
}

func makeRequest(baseURL, header, proxy string, delay int) (*http.Response, error) {
	// Apply delay before making the request
	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Second)
	}

	urlWithBuster := baseURL + "?cachebuster=" + generateCacheBuster()  // Adds a cachebuster query parameter
	headers := parseHeaders(header)  // Parses the headers into a slice of strings

	// Create a new HTTP GET request
	req, err := http.NewRequest("GET", urlWithBuster, nil)
	if err != nil {
		return nil, err
	}

	// Add the parsed headers to the request
	for _, h := range headers {
		parts := strings.SplitN(h, ": ", 2)
		if len(parts) == 2 {
			req.Header.Add(parts[0], parts[1])
		}
	}

	// Create an HTTP client
	client := &http.Client{}
	if proxy != "" {
		// If a proxy is provided, configure the client to use it
		proxyURL, err := url.Parse("http://" + proxy)
		if err != nil {
			fmt.Println("Error parsing proxy URL:", err)
			return nil, err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		client = &http.Client{Transport: transport}
	}

	// Send the HTTP request and return the response
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Check if the content length is available, read body if necessary
	if response.ContentLength >= 0 {
		return response, nil
	}

	// If ContentLength is not provided, read the response body to calculate it
	body, err := io.ReadAll(response.Body)
	if err == nil {
		response.ContentLength = int64(len(body))
	}
	return response, nil
}

func parseHeaders(header string) []string {
	return strings.Split(header, "\n")
}

func generateCacheBuster() string {
	rand.Seed(time.Now().UnixNano())
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 10)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func applyBackoff(response *http.Response, backoff int) int {
	retryAfter := response.Header.Get("Retry-After")
	if retryAfter != "" {
		if retrySecs, err := strconv.Atoi(retryAfter); err == nil {
			time.Sleep(time.Duration(retrySecs) * time.Second)
			return backoff
		}
	}

	// Exponential backoff if Retry-After is not specified
	time.Sleep(time.Duration(backoff) * time.Second)
	return backoff * 2 // Double the backoff time for the next request
}

func printResults(results <-chan Result, foundOnly bool) {
	red := color.New(color.FgRed).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	magenta := color.New(color.FgMagenta).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	cyan := color.New(color.FgCyan).SprintFunc()

	for result := range results {
		// Only print results with status code 200 if the -found flag is set
		if foundOnly && result.StatusCode != 200 {
			continue
		}

		statusColorFunc := red
		if result.StatusCode == 200 {
			statusColorFunc = green
		}

		statusOutput := statusColorFunc(fmt.Sprintf("[%d]", result.StatusCode))
		contentLengthOutput := magenta(fmt.Sprintf("[CL: %d]", result.ContentLength))
		headerOutput := cyan(fmt.Sprintf("[%s]", result.Header))

		parsedURL, _ := url.Parse(result.URL)
		query := parsedURL.Query()
		query.Del("cachebuster")
		parsedURL.RawQuery = query.Encode()
		urlOutput := yellow(fmt.Sprintf("[%s]", parsedURL.String()))

		resultOutput := fmt.Sprintf("%s %s %s %s", statusOutput, contentLengthOutput, headerOutput, urlOutput)
		fmt.Println(resultOutput)
	}
}
