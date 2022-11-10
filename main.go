package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func randStr(n int) (ret string) {
	const POOL = "1234567890qwertyuiopasdfghjklzxcvbnmQWERTYUIOPASDFGHJKLZXCVBNM"
	for i := 0; i < n; i++ {
		ret += string(POOL[rand.Intn(len(POOL))])
	}
	return
}

func sendPhoto(c *http.Client, token, chatId, filename, caption string, photo []byte) (*http.Response, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	wr, _ := w.CreateFormField("chat_id")
	wr.Write([]byte(chatId))
	wr, _ = w.CreateFormFile("photo", filename)
	wr.Write(photo)
	if len(caption) > 0 {
		wr, _ = w.CreateFormField("caption")
		wr.Write([]byte(caption))
	}
	w.Close()

	req, err := http.NewRequest("POST", "https://api.telegram.org/bot"+token+"/sendPhoto", &b)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.Do(req)
}

func vnstat(mode string) ([]byte, error) {
	path := os.TempDir() + "/" + randStr(32) + ".png"

	cmd := exec.Command(
		"vnstati",
		"-L",
		"-o",
		path,
		"-"+mode,
	)
	fmt.Printf("Executing: [%s]\n", cmd.String())
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	defer f.Close()
	defer os.Remove(path)
	if err != nil {
		return nil, err
	}

	pic, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	return pic, nil
}

func sendOne(c *http.Client) {
	pic, err := vnstat(defaultMode)
	if err != nil {
		log.Printf("Error at cron: %s\n", err.Error())
		return
	}
	resp, err := sendPhoto(c, token, chatId, "vnstat.png", time.Now().Format(time.RFC1123), pic)
	rr, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error at reading resp: %s\n", err.Error())
		return
	}
	log.Printf("Successfully sent: %s\n", string(rr))
}

func cron(interval time.Duration) {
	c := &http.Client{}
	for {
		sendOne(c)
		time.Sleep(interval)
	}
}

var (
	token       string
	chatId      string
	admins      map[string]bool
	defaultMode string
)

type upd struct {
	Ok     bool                     `json:"ok"`
	Result []map[string]interface{} `json:"result"`
}

func handleIncomingUpdate(obj map[string]interface{}) {
	var m map[string]interface{}
	if msg, ok := obj["message"]; ok {
		m = msg.(map[string]interface{})
	} else {
		log.Printf("No Message!\n")
		return
	}

	text, ok := m["text"].(string)
	if !ok {
		log.Printf("text is not string, abort")
		return
	}

	from, ok := m["from"].(map[string]interface{})
	if !ok {
		log.Printf("cannot parse 'from', abort")
		return
	}

	var from_id string
	if fid, ok := from["id"]; ok {
		if fs, ok := fid.(string); ok {
			from_id = fs
		} else {
			from_id = strconv.FormatInt(int64(fid.(float64)), 10)
		}
	} else {
		log.Printf("Invalid from ID\n")
		return
	}
	if !admins[from_id] {
		log.Printf("Non admin: %s\n", from_id)
		return
	}

	chat := m["chat"].(map[string]interface{})
	const AVAIL = "5 5g h hg d m y t s hs vs "
	if strings.Contains(AVAIL, text[1:]+" ") {
		c := &http.Client{}
		pic, err := vnstat(text[1:])

		if err != nil {
			log.Printf("vnstati Error: %s\n", err.Error())
			return
		}

		this_chat_id := strconv.FormatInt(int64(chat["id"].(float64)), 10)
		resp, err := sendPhoto(c, token, this_chat_id, "v.png", time.Now().Format(time.RFC1123), pic)
		if err != nil {
			log.Printf("send telegram Error: %s\n", err.Error())
			return
		}
		rr, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error at reading resp: %s\n", err.Error())
			return
		}
		log.Printf("Successfully sent: %s\n", string(rr))
	} else {
		log.Printf("Ignoring: %s\n", text)
	}
}

func handleTelegram(c *gin.Context) {
	var obj map[string]interface{}
	c.BindJSON(&obj)

	defer c.JSON(http.StatusOK, gin.H{
		"message": "pong",
	})

	handleIncomingUpdate(obj)
}

func polling() {
	var updateId int64
	c := http.Client{
		Timeout: 65 * time.Second,
	}
	for {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=60", token, updateId)
		resp, err := c.Get(url)
		if err != nil {
			log.Printf("Error polling: %s\n", err.Error())
			continue
		}
		r, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading polling: %s\n", err.Error())
			continue
		}
		var o upd
		err = json.Unmarshal(r, &o)
		if err != nil {
			log.Printf("Error parsing JSON: %s\n", err.Error())
		} else if len(o.Result) > 0 {
			for _, v := range o.Result {
				nui := int64(v["update_id"].(float64))
				if updateId < nui+1 {
					updateId = nui + 1
				}
				go handleIncomingUpdate(v)
			}
		}
		log.Printf("End of polling cycle.")
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// Read config
	conf, err := os.Open("vnstati_config.json")
	if err != nil {
		panic(err)
	}
	confByte, err := io.ReadAll(conf)
	if err != nil {
		panic(err)
	}
	var c map[string]interface{}
	err = json.Unmarshal(confByte, &c)
	if err != nil {
		panic(err)
	}

	token = c["token"].(string)
	chatId = c["chat_id"].(string)
	bind := c["listen"].(string)
	defaultMode = c["default_mode"].(string)
	interval, err := strconv.ParseInt(c["interval"].(string), 10, 64)
	if err != nil {
		panic(err)
	}

	adminStr := c["admins"].([]interface{})
	admins = make(map[string]bool)
	for _, v := range adminStr {
		admins[v.(string)] = true
	}

	fmt.Printf("Telegram bot token: [%s]\n", token)
	fmt.Printf("Regular report sends to: [%s]\n", chatId)
	fmt.Printf("Report interval: [%d seconds]\n", interval)
	fmt.Printf("Default Mode: [%s]\n", defaultMode)
	fmt.Printf("Admins: [%s]\n", func() (ret string) {
		for k, _ := range admins {
			if len(ret) > 0 {
				ret += ", " + k
			} else {
				ret += k
			}
		}
		return
	}())

	go cron(time.Duration(interval) * time.Second)

	r := gin.Default()
	r.TrustedPlatform = gin.PlatformCloudflare
	gin.SetMode(gin.ReleaseMode)
	r.POST("/C9eKiEvF", handleTelegram)
	r.Run(bind)

	polling()
}
