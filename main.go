package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"

	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mediocregopher/radix.v2/redis"
)

var (
	token           = flag.String("slack_webhook", "", "slack webhook url")
	redisURL        = flag.String("redis_url", "127.0.0.1:6379", "redis url")
	expiration      = flag.Int64("expiration", 180, "expiration time in second")
	freq            = flag.Int64("frequency", 60, "alert frequence in second")
	listen          = flag.String("listen_addr", "0.0.0.0:19093", "listen address")
	silenceDuration = flag.Int64("silence_duration", 60*60, "silence duration")
	externalURL     = flag.String("external_url", "", "URL under which molert is externally reachable.")
	redisClient     *redis.Client
)

type Alert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short,omitempty"`
}

type Attachment struct {
	Fallback   string  `json:"fallback,omitempty"`
	Color      string  `json:"color,omitempty"`
	Pretext    string  `json:"pretext,omitempty"`
	AuthorName string  `json:"author_name,omitempty"`
	AuthorLink string  `json:"author_link,omitempty"`
	Title      string  `json:"title,omitempty"`
	TitleLink  string  `json:"title_link,omitempty"`
	Text       string  `json:"text,omitempty"`
	Fields     []Field `json:"fields,omitempty"`
	ImageURL   string  `json:"image_url,omitempty"`
	ThumbURL   string  `json:"thumb_url,omitempty"`
	Footer     string  `json:"footer,omitempty"`
	FooterIcon string  `json:"footer_icon,omitempty"`
	Timestamp  int64   `json:"ts,omitempty"`
}

type Payload struct {
	Text        string       `json:"text,omitempty"`
	Channel     string       `json:"channel,omitempty"`
	Username    string       `json:"username,omitempty"`
	IconEmoji   string       `json:"icon_emoji,omitempty"`
	IconURL     string       `json:"icon_url,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Silence struct {
	URL      string `json:"url"`
	Duration int64  `json:"duration,omitempty"`
}

func init() {
	flag.Parse()
	var err error
	var url string
	if *redisURL == "" {
		url = os.Getenv("REDIS_URL")
	} else {
		url = *redisURL
	}
	redisClient, err = redis.Dial("tcp", url)
	if err != nil {
		log.Fatalf("failed to connect redis: %s", url)
	}
}

func main() {
	ticker := time.NewTicker(time.Second * time.Duration(*freq))
	go func() {
		for _ = range ticker.C {
			alert()
		}
	}()
	log.Printf("listening on %s", *listen)
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/silence", silenceHandler)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

func alert() {
	r := redisClient.Cmd("SMEMBERS", "alert_urls")
	if r.Err != nil {
		log.Print(r.Err)
		return
	}
	urls, err := r.List()
	if err != nil {
		log.Printf("expected alert url list from %v", r)
		return
	}
	for _, url := range urls {
		res := redisClient.Cmd("HMGET", url, "alert", "silence")
		if res.Err != nil {
			log.Print(res.Err)
			continue
		}
		result, err := res.List()
		if err != nil {
			log.Printf("expected alert payload and silence from %v", res)
			continue
		}
		if len(result) == 2 && result[0] == "" { // if empty alert, url should be removed from alert_urls set
			resp := redisClient.Cmd("SREM", "alert_urls", url)
			log.Printf("remove %s from alert_urls: %v", url, resp)
			continue
		}
		if len(result) == 2 && result[1] != "true" {
			var a Alert
			err := json.Unmarshal([]byte(result[0]), &a)
			if err != nil {
				log.Printf("failed to unmarshal %s to Alert", result[0])
				continue
			}
			payloads := a.toPayloads()
			for _, payload := range payloads {
				payload.send()
			}
		}
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
	}
	var alerts []Alert
	err = json.Unmarshal(body, &alerts)
	if err != nil {
		log.Printf("failed to unmarshal incoming %s to []Alert", body)
	}
	for _, alert := range alerts {
		alert.save()
	}
	w.Write([]byte("ok"))
}

func silenceHandler(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Print(err)
	}
	defer r.Body.Close()
	var s Silence
	err = json.Unmarshal(body, &s)
	if err != nil {
		log.Printf("failed to unmarshal incoming %s to Silence", body)
	}
	s.silence()
	w.Write([]byte("ok"))
}

func (a *Alert) toPayloads() []Payload {
	attachment := Attachment{
		Color:     "warning",
		TitleLink: a.GeneratorURL,
		Timestamp: a.StartsAt.Unix(),
	}
	if summary, found := a.Annotations["summary"]; found {
		attachment.Title = summary
	}
	if description, found := a.Annotations["description"]; found {
		attachment.Text = description
	}

	s, _ := json.Marshal(Silence{URL: a.GeneratorURL, Duration: *silenceDuration})
	silenceCmd := fmt.Sprintf("curl -XPOST -H 'Content-Type: application/json' -d '%s'", s)

	var payloads []Payload
	if users, found := a.Labels["users"]; found {
		for _, user := range strings.Split(strings.TrimSpace(users), ",") {
			p := Payload{
				Username:    "alert-bot",
				IconEmoji:   ":loudspeaker:",
				Text:        silenceCmd,
				Attachments: []Attachment{attachment},
				Channel:     fmt.Sprintf("@%s", strings.TrimSpace(user)),
			}
			payloads = append(payloads, p)
		}
	}
	if channels, found := a.Labels["channels"]; found {
		for _, ch := range strings.Split(strings.TrimSpace(channels), ",") {
			p := Payload{
				Username:    "alert-bot",
				IconEmoji:   ":loudspeaker:",
				Text:        silenceCmd,
				Attachments: []Attachment{attachment},
				Channel:     strings.TrimSpace(ch),
			}
			payloads = append(payloads, p)
		}
	}
	return payloads
}

func (p *Payload) send() {
	data, err := json.Marshal(p)
	if err != nil {
		log.Printf("failed to marshal %+v, alert would not sent", p)
		return
	}
	_, err = http.Post(*token, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("failed to send alert %s: %v", data, err)
	}
}

// save alert to redis
func (a *Alert) save() {
	data, err := json.Marshal(a)
	if err != nil {
		log.Printf("failed to marshal %+v: %s", a, err.Error())
		return
	}
	resp := redisClient.Cmd("SADD", "alert_urls", a.GeneratorURL)
	statusCode, err := resp.Int() // should return Int 1
	if err != nil {
		log.Printf("failed to save alert %s: %s", a.GeneratorURL, err.Error())
		return
	}
	// check alert status
	resp = redisClient.Cmd("HGET", a.GeneratorURL, "silence")
	r, err := resp.Str()
	if err == nil && r == "true" {
		log.Printf("alert %s already silenced, this will be ignored", a.GeneratorURL)
		return
	}
	// add alert to redis
	resp = redisClient.Cmd("HMSET", a.GeneratorURL, map[string]string{
		"alert":   string(data),
		"silence": "false",
	})
	status, err := resp.Str() // should return Str "OK"
	if err != nil {
		log.Printf("failed to save alert %s: %s", a.GeneratorURL, err.Error())
		return
	}
	if status == "OK" {
		log.Print("added successfully")
	}
	// check alert ttl
	resp = redisClient.Cmd("TTL", a.GeneratorURL)
	ttl, err := resp.Int()
	if err == nil && ttl >= 0 {
		log.Printf("expiration for %s already set to %d, this will be ignored", a.GeneratorURL, ttl)
		return
	}
	// set expiration
	resp = redisClient.Cmd("EXPIRE", a.GeneratorURL, *expiration)
	statusCode, err = resp.Int()
	if err != nil {
		log.Printf("failed to set expiration for %s: %s", a.GeneratorURL, err.Error())
		return
	}
	if statusCode == 1 {
		log.Printf("expiration for %s set successfully", a.GeneratorURL)
	}
}

// silence make alert silence
func (s *Silence) silence() {
	resp := redisClient.Cmd("HSET", s.URL, "silence", "true")
	statusCode, err := resp.Int()
	if err != nil {
		log.Printf("failed to silence alert %s: %s", s.URL, err.Error())
		return
	}
	if statusCode == 1 {
		log.Printf("alert %s was silenced successfully", s.URL)
	}
	if s.Duration < 0 {
		log.Printf("silenced %s forever", s.URL)
		return // silence forever
	}
	if s.Duration == 0 { // silence for default duration
		resp = redisClient.Cmd("EXPIRE", s.URL, *silenceDuration)
		log.Printf("silenced %s for default duration", s.URL)
		return
	}
	// silence for given duration, use small positive integer(eg. 1) to un-silence an alert
	resp = redisClient.Cmd("EXPIRE", s.URL, s.Duration)
	log.Printf("silenced %s for %d seconds", s.URL, s.Duration)
}
