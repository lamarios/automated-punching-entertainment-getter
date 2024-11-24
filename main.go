package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/anaskhan96/soup"
	"github.com/autobrr/go-qbittorrent"
	"github.com/gregdel/pushover"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type Event struct {
	Url                    string    `json:"url"`
	Name                   string    `json:"name"`
	Date                   time.Time `json:"date"`
	EarlyPrelimsDownloaded bool      `json:"earlyPrelimsDownloaded"`
	PrelimsDownloaded      bool      `json:"prelimsDownloaded"`
	MainCardDownloaded     bool      `json:"mainCardDownloaded"`
}

type ProwlarrTorrent struct {
	Guid      string `json:"guid"`
	Title     string `json:"sortTitle"`
	Seeders   int    `json:"seeders"`
	MagnetUrl string `json:"magnetUrl"`
}

type EventType struct {
	Name   string
	Search string
	ignore []string
}

type ProwlarrConfig struct {
	Url    string `yaml:"url"`
	ApiKey string `yaml:"apiKey"`
}

type QbitorrentConfig struct {
	Url      string `yaml:"url"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type PushoverConfig struct {
	Token   string `yaml:"token"`
	UserKey string `yaml:"userKey"`
}

type Config struct {
	DataFile   string           `yaml:"dataFile"`
	Prowlarr   ProwlarrConfig   `yaml:"prowlarr"`
	Qbitorrent QbitorrentConfig `yaml:"qbitorrent"`
	Pushover   PushoverConfig   `yaml:"pushover"`
}

var (
	EarlyPrelims EventType = EventType{Name: "Early Prelims", Search: "1080p early prelims", ignore: []string{"breakdown", "ppv", "embedded", "countdown"}}
	Prelims      EventType = EventType{Name: "Prelims", Search: "1080p prelims", ignore: []string{"breakdown", "ppv", "embedded", "countdown", "early"}}
	MainCard     EventType = EventType{Name: "Main card", Search: "1080p ", ignore: []string{"breakdown", "embedded", "countdown", "prelims"}}
	UserConfig   Config    = Config{}
)

//TIP To run your code, right-click the code and select <b>Run</b>. Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.

func main() {

	config, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	UserConfig = config

	last, next, err := getLastAndNextEvent()

	if err != nil {
		log.Fatal(err)
	}

	err = downloadEvent(last)
	if err != nil {
		log.Fatal(err)
	}

	err = downloadEvent(next)
	if err != nil {
		log.Fatal(err)
	}

}

func sendDownloadNotification(event Event, eventType EventType) error {
	// Create a new pushover app with a Token
	app := pushover.New(UserConfig.Pushover.Token)

	// Create a new recipient
	recipient := pushover.NewRecipient(UserConfig.Pushover.UserKey)

	// Create the message to send
	message := pushover.NewMessageWithTitle(eventType.Name+" sent to download client", event.Name)

	// Send the message to the recipient
	_, err := app.SendMessage(message, recipient)
	if err != nil {
		return err
	}

	return nil
}

func loadConfig() (config Config, err error) {
	configFile := os.Getenv("CONFIG_FILE")
	if len(configFile) == 0 {
		configFile = "./config.yaml"
	}
	// Load the JSON file into a byte array
	file, err := os.Open(configFile)
	if err != nil {
		return
	}
	defer file.Close()

	// Create a new YAML decoder
	decoder := yaml.NewDecoder(file)

	// Unmarshal the YAML data into a Person struct
	err = decoder.Decode(&config)
	if err != nil {
		return
	}

	return
}

func loadEvents() (events map[string]Event, err error) {
	events = make(map[string]Event)

	// Load the JSON file into a byte array
	file, err := os.Open(UserConfig.DataFile)
	if err != nil {
		return
	}
	defer file.Close()

	// Create a new YAML decoder
	decoder := json.NewDecoder(file)

	// Unmarshal the YAML data into a Person struct
	err = decoder.Decode(&events)
	if err != nil {
		return
	}

	return

}

func saveEvents(events map[string]Event) error {
	jsonString, err := json.Marshal(events)
	err = os.WriteFile(UserConfig.DataFile, jsonString, os.ModePerm)
	// Encode the Person struct into YAML format
	//err = encoder.Encode(events)
	if err != nil {
		return err
	}

	return nil
}

func downloadEvent(event Event) (err error) {

	events, err := loadEvents()
	if err != nil {
		events = make(map[string]Event)
	}

	if val, ok := events[event.Url]; ok {
		event = val
	}

	// if we already downloaded everything, we skip all
	if event.MainCardDownloaded && event.PrelimsDownloaded && event.EarlyPrelimsDownloaded {
		return nil
	}

	results, err := searchProwlarr(event)

	if !event.EarlyPrelimsDownloaded {
		earlyPrelims, err := findBestTorrents(event, results, EarlyPrelims)
		if err == nil {
			err = addTorrent(earlyPrelims)
			if err == nil {
				event.EarlyPrelimsDownloaded = true
				sendDownloadNotification(event, EarlyPrelims)
			}
		}
	}

	if !event.PrelimsDownloaded {
		prelims, err := findBestTorrents(event, results, Prelims)
		if err == nil {
			err = addTorrent(prelims)
			if err == nil {
				event.PrelimsDownloaded = true
				sendDownloadNotification(event, Prelims)
			}
		}
	}

	if !event.MainCardDownloaded {
		mainCard, err := findBestTorrents(event, results, MainCard)
		if err == nil {
			err = addTorrent(mainCard)
			if err == nil {
				event.MainCardDownloaded = true
				sendDownloadNotification(event, MainCard)
			}
		}
	}
	events[event.Url] = event
	err = saveEvents(events)
	return
}

func addTorrent(torrent ProwlarrTorrent) error {
	client := qbittorrent.NewClient(qbittorrent.Config{
		Host:     UserConfig.Qbitorrent.Url,
		Username: UserConfig.Qbitorrent.User,
		Password: UserConfig.Qbitorrent.Password,
	})

	ctx := context.Background()

	if err := client.LoginCtx(ctx); err != nil {
		return err
	}

	if len(torrent.MagnetUrl) > 0 {
		options := make(map[string]string)
		options["category"] = "ufc-downloader"
		options["autoTMM"] = "true"
		err := client.AddTorrentFromUrl(torrent.MagnetUrl, options)
		if err != nil {
			return err
		}
	}

	return nil
}

func findMagnetLink(torrent ProwlarrTorrent) (magnetUrl string, err error) {
	if strings.Index(torrent.Guid, "magnet:?xt=") != -1 {
		magnetUrl = torrent.Guid
		return
	}

	// we load the url of the page and get the magnet link
	resp, err := soup.Get(torrent.Guid)
	if err != nil {
		return
	}
	doc := soup.HTMLParse(resp).FindAll("a")

	for _, l := range doc {
		if strings.Index(l.Attrs()["href"], "magnet:?xt=") != -1 {
			magnetUrl = l.Attrs()["href"]
			return
		}

	}

	err = errors.New("No magnet link found")
	return
}

// Find best torrent for a type of event
func findBestTorrents(event Event, torrents []ProwlarrTorrent, eventType EventType) (best ProwlarrTorrent, err error) {
	fmt.Printf("Finding torrent for %s  %s \n", event.Name, eventType)
	searchString := strings.Split(strings.TrimSpace(event.Name+" "+eventType.Search), " ")
	fmt.Println(searchString)

	matchingTorrents := make([]ProwlarrTorrent, 0)

	for _, t := range torrents {
		containsAll := true
		for _, s := range searchString {
			if !strings.Contains(t.Title, s) {
				containsAll = false
			}
		}

		// check for ignore words
		if containsAll {
			ignore := false
			for _, s := range eventType.ignore {
				if strings.Contains(t.Title, s) {
					ignore = true
				}
			}

			if !ignore {
				matchingTorrents = append(matchingTorrents, t)
			}
		}
	}

	sort.Slice(matchingTorrents, func(i, j int) bool {
		return matchingTorrents[i].Seeders > matchingTorrents[j].Seeders
	})

	if len(matchingTorrents) > 0 {
		for _, t := range matchingTorrents {
			magnetUrl, _ := findMagnetLink(t)
			if len(magnetUrl) > 0 {
				best = matchingTorrents[0]
				best.MagnetUrl = magnetUrl
				return
			}
		}
	}

	err = errors.New("Couldn't find best torrent")

	return

}

func searchProwlarr(event Event) (torrents []ProwlarrTorrent, err error) {

	fmt.Println("Getting prowlarr results for " + event.Name)
	queryUrl := UserConfig.Prowlarr.Url + "/api/v1/search?query=" + url.QueryEscape(event.Name) + "&type=Search&limit=100&offset=0&apikey=" + UserConfig.Prowlarr.ApiKey

	resp, err := http.Get(queryUrl)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	// Unmarshal the JSON response into a Person struct
	err = json.Unmarshal(body, &torrents)
	if err != nil {
		return
	}
	fmt.Printf("Found %d torrents\n", len(torrents))

	return
}

func getLastAndNextEvent() (lastEvent Event, nextEvent Event, err error) {
	resp, err := soup.Get("https://www.sherdog.com/organizations/Ultimate-Fighting-Championship-UFC-2")
	if err != nil {
		return
	}
	doc := soup.HTMLParse(resp)
	// Getting next event
	upcomingEvents := doc.Find("div", "id", "upcoming_tab")

	nextEvent, err = parseEvent(upcomingEvents)
	if err != nil {
		return
	}

	// getting last event
	recentEvents := doc.Find("div", "id", "recent_tab")

	lastEvent, err = parseEvent(recentEvents)
	if err != nil {
		return
	}

	return
}

func parseEvent(table soup.Root) (Event, error) {
	rows := table.Find("tr", "itemtype", "http://schema.org/Event")
	name := rows.Find("span", "itemprop", "name")
	date := rows.Find("meta", "itemprop", "startDate").Attrs()["content"]
	eventUrl := rows.Find("a", "itemprop", "url").Attrs()["href"]

	layout := "2006-01-02T15:04:05Z07:00"
	t, err := time.Parse(layout, date)
	if err != nil {
		fmt.Println("Error parsing date:", err)
		return Event{}, err
	}

	parts := strings.Split(name.Text(), "-")
	return Event{
		Date: t,
		Name: strings.ToLower(strings.TrimSpace(parts[0])),
		Url:  eventUrl,
	}, nil
}
