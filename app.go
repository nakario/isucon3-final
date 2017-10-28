package main

import (
	"github.com/google/uuid"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"time"
	imagepkg "image"
	"image/png"
	"image/jpeg"
	"github.com/oliamb/cutter"
	"github.com/nfnt/resize"

	_ "net/http/pprof"
	"bytes"
)

const (
	listenAddr = ":5000"

	timeout  = 30
	interval = 2

	iconS  = 32
	iconM  = 64
	iconL  = 128
	imageS = 128
	imageM = 256
	imageL = -1
)

var (
	dbConn  *sql.DB
	config  *Config
	exp3 = regexp.MustCompile("^[a-zA-Z0-9_]{2,16}$")
)

type Config struct {
	Database struct {
		Dbname   string `json:"dbname"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"database"`
	Datadir string `json:"data_dir"`
}

type User struct {
	Id     int
	Name   string
	Apikey string
	Icon   string
}

type Entry struct {
	Id           int
	User         int
	Image        string
	PublishLevel int
	CreatedAt    string
}

type FollowMap struct {
	User      int
	Target    int
	CreatedAt string
}

type Response map[string]interface{}

func (r Response) String() (s string) {
	b, err := json.Marshal(r)
	if err != nil {
		s = ""
	} else {
		s = string(b)
	}
	return
}

func prepareHandler(w http.ResponseWriter, r *http.Request) (baseUrl *url.URL) {
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		baseUrl, _ = url.Parse("http://" + h)
	} else {
		baseUrl, _ = url.Parse("http://" + r.Host)
	}
	return baseUrl
}

func getUser(r *http.Request) (*User, error) {
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		c, err := r.Cookie("api_key")
		if err != nil {
			return nil, nil
		} else {
			apiKey = c.Value
		}
	}

	user := User{}
	err := dbConn.QueryRow(
		"SELECT * FROM users WHERE api_key = ?", apiKey,
	).Scan(
		&user.Id, &user.Name, &user.Apikey, &user.Icon,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &user, nil
}

func loadConfig(filename string) *Config {
	log.Printf("loading config file: %s", filename)
	f, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	var config Config
	err = json.Unmarshal(f, &config)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	return &config
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	rand.Seed(time.Now().Unix())

	env := os.Getenv("ISUCON_ENV")
	if env == "" {
		env = "local"
	}
	config = loadConfig("../config/" + env + ".json")

	db := config.Database
	connectionString := fmt.Sprintf(
		"%s:%s@unix(/var/lib/mysql/mysql.sock)/%s?charset=utf8",
		db.Username, db.Password, db.Dbname,
	)
	log.Printf("db: %s", connectionString)
	var err error
	dbConn, err = sql.Open("mysql", connectionString)
	if err != nil {
		log.Panicf("Error opening database: %v", err)
	}

	r := mux.NewRouter()
	r.HandleFunc("/signup", signupHandler).Methods("POST")
	r.HandleFunc("/me", meHandler).Methods("GET")
	r.HandleFunc("/entry/{id}", deleteEntryHandler).Methods("POST")
	r.HandleFunc("/entry", entryHandler).Methods("POST")
	r.HandleFunc("/timeline", timelineHandler).Methods("GET")
	r.HandleFunc("/icon/{icon}", iconHandler).Methods("GET")
	r.HandleFunc("/icon", updateIconHandler).Methods("POST")
	r.HandleFunc("/image/{image}", imageHandler).Methods("GET")
	r.HandleFunc("/follow", followingHandler).Methods("GET")
	r.HandleFunc("/follow", followHandler).Methods("POST")
	r.HandleFunc("/unfollow", unfollowHandler).Methods("POST")
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./public/")))
	http.Handle("/", r)
	http.ListenAndServe(listenAddr, nil)
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %s", err)
	code := http.StatusInternalServerError
	http.Error(w, http.StatusText(code), code)
}

func notFound(w http.ResponseWriter) {
	code := http.StatusNotFound
	http.Error(w, http.StatusText(code), code)
}

func badRequest(w http.ResponseWriter) {
	code := http.StatusBadRequest
	http.Error(w, http.StatusText(code), code)
}

func join(a ...interface{}) string {
	var ret string
	for _, v := range a {
		ret += fmt.Sprintf("%v", v)
	}
	return ret
}

func sha256Hex(a ...interface{}) string {
	hash := sha256.New()
	hash.Write([]byte(join(a...)))
	md := hash.Sum(nil)
	return hex.EncodeToString(md)
}

func convert(data []byte, ext string, w int, h int) ([]byte, error) {
	image, _, err := imagepkg.Decode(bytes.NewReader(data))
	if err != nil {
		log.Println("Cannot decode")
		return nil, err
	}
	resized := resize.Resize(uint(w), uint(h), image, resize.Lanczos3)
	b := []byte{}
	buf := bytes.NewBuffer(b)
	if ext == "jpg" {
		err := jpeg.Encode(buf, resized, nil)
		if err != nil {
			log.Println("Failed encoding jpg")
			return nil, err
		}
	} else if ext == "png" {
		err := png.Encode(buf, resized)
		if err != nil {
			log.Println("Failed encoding png")
			return nil, err
		}
	} else {
		log.Println("Unexpected ext " + ext)
		return nil, err
	}
	log.Println("Converted size is ", len(buf.Bytes()))

	return buf.Bytes(), nil
}

func cropSquare(image imagepkg.Image, ext string) ([]byte, error) {
	w := image.Bounds().Size().X
	h := image.Bounds().Size().Y
	var crop_x float32
	var crop_y float32
	var pixels int
	if w > h {
		pixels = h
		crop_x = (float32(w-pixels) / 2)
		crop_y = 0
	} else if w < h {
		pixels = w
		crop_x = 0
		crop_y = (float32(h-pixels) / 2)
	} else {
		pixels = w
		crop_x = 0
		crop_y = 0
	}

	log.Println("Crop width:", pixels, "height:", pixels, "anchor x:", int(crop_x), "anchor y:", int(crop_y))
	cropped, err := cutter.Crop(image, cutter.Config{Width: pixels, Height: pixels, Anchor: imagepkg.Pt(int(crop_x), int(crop_y))})
	if err != nil {
		return nil, err
	}
	b := []byte{}
	buf := bytes.NewBuffer(b)
	if ext == "jpg" {
		err := jpeg.Encode(buf, cropped, nil)
		if err != nil {
			return nil, err
		}
	} else if ext == "png" {
		err := png.Encode(buf, cropped)
		if err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

func renderJson(w http.ResponseWriter, r Response) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, r)
}

func renderJsonNoCache(w http.ResponseWriter, r Response) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, r)
}

func signupHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	name := r.FormValue("name")

	if !exp3.MatchString(name) {
		badRequest(w)
		return
	}

	apiKey := sha256Hex(uuid.NewUUID())

	result, err := dbConn.Exec(
		"INSERT INTO users (name, api_key, icon) VALUES (?, ?, ?)",
		name, apiKey, "default",
	)
	if err != nil {
		serverError(w, err)
		return
	}

	id, _ := result.LastInsertId()
	user := User{}
	err = dbConn.QueryRow(
		"SELECT * FROM users WHERE id = ?", id,
	).Scan(
		&user.Id, &user.Name, &user.Apikey, &user.Icon,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{
		"id":      user.Id,
		"name":    user.Name,
		"api_key": user.Apikey,
		"icon":    baseUrl.String() + "/icon/" + user.Icon,
	})
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	renderJson(w, Response{
		"id":   user.Id,
		"name": user.Name,
		"icon": baseUrl.String() + "/icon/" + user.Icon,
	})
}

func entryHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	uploadFile, handler, err := r.FormFile("image")
	if err != nil {
		serverError(w, err)
		return
	}
	if handler == nil {
		badRequest(w)
		return
	}

	contentType := handler.Header.Get("Content-Type")
	if !(contentType == "image/jpeg" || contentType == "image/jpg") {
		badRequest(w)
		return
	}

	data, err := ioutil.ReadAll(uploadFile)
	if err != nil {
		serverError(w, err)
		return
	}

	imageId := sha256Hex(uuid.NewUUID())
	err = ioutil.WriteFile(config.Datadir+"/image/"+imageId+".jpg", data, 0666)
	if err != nil {
		serverError(w, err)
		return
	}

	publishLevel := r.FormValue("publish_level")
	result, err := dbConn.Exec(
		"INSERT INTO entries (user, image, publish_level, created_at) VALUES (?, ?, ?, NOW())",
		user.Id, imageId, publishLevel,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		serverError(w, err)
		return
	}

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE id = ?", id,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{
		"id":            entry.Id,
		"image":         baseUrl.String() + "/image/" + entry.Image,
		"publish_level": entry.PublishLevel,
		"user": Response{
			"id":   user.Id,
			"name": user.Name,
			"icon": baseUrl.String() + "/icon/" + user.Icon,
		},
	})
}

func timelineHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	latestEntryId, err := strconv.Atoi(r.FormValue("latest_entry"))
	if err != nil {
		latestEntryId = 0
	}

	timeoutMessage := make(chan bool)
	entriesMessage := make(chan []Response)

	go func() {
		time.Sleep(time.Second * timeout)
		timeoutMessage <- true
	}()

	go func() {
		for {
			var (
				rows *sql.Rows
				err  error
			)
			if 0 < latestEntryId {
				rows, err = dbConn.Query(
					"SELECT * FROM (SELECT * FROM entries WHERE (user=? OR publish_level=2 OR (publish_level=1 AND user IN (SELECT target FROM follow_map WHERE user=?))) AND id > ? ORDER BY id LIMIT 30) AS e ORDER BY e.id DESC",
					user.Id, user.Id, latestEntryId,
				)
			} else {
				rows, err = dbConn.Query(
					"SELECT * FROM entries WHERE (user=? OR publish_level=2 OR (publish_level=1 AND user IN (SELECT target FROM follow_map WHERE user=?))) ORDER BY id DESC LIMIT 30",
					user.Id, user.Id,
				)
			}
			if err != nil {
				serverError(w, err)
				return
			}
			entries := []Entry{}
			for rows.Next() {
				entry := Entry{}
				rows.Scan(&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt)
				entries = append(entries, entry)
			}
			rows.Close()
			if 0 < len(entries) {
				res := []Response{}
				for _, entry := range entries {
					user := User{}
					err = dbConn.QueryRow(
						"SELECT * FROM users WHERE id = ?", entry.User,
					).Scan(
						&user.Id, &user.Name, &user.Apikey, &user.Icon,
					)
					if err != nil {
						serverError(w, err)
						return
					}
					res = append(res, Response{
						"id":            entry.Id,
						"image":         baseUrl.String() + "/image/" + entry.Image,
						"publish_level": entry.PublishLevel,
						"user": Response{
							"id":   user.Id,
							"name": user.Name,
							"icon": baseUrl.String() + "/icon/" + user.Icon,
						},
					})
				}
				latestEntryId = entries[0].Id
				entriesMessage <- res
				return
			}
			time.Sleep(time.Second * interval)
		}
	}()

	select {
	case entries := <-entriesMessage:
		renderJsonNoCache(w, Response{
			"latest_entry": latestEntryId,
			"entries":      entries,
		})
		return
	case <-timeoutMessage:
		renderJsonNoCache(w, Response{
			"latest_entry": latestEntryId,
			"entries":      []Entry{},
		})
		return
	}
}

func iconHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	vars := mux.Vars(r)
	icon := vars["icon"]

	if _, err := os.Stat(config.Datadir + "/icon/" + icon + ".png"); os.IsNotExist(err) {
		notFound(w)
		return
	}

	size := r.FormValue("size")
	if size == "" {
		size = "s"
	}

	var width int
	var height int
	if size == "s" {
		width = iconS
	} else if size == "m" {
		width = iconM
	} else if size == "l" {
		width = iconL
	} else {
		width = iconS
	}
	height = width

	filename := "/home/isucon/static/icon/" + size + "/" + icon + ".png"
	var data []byte

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		file, err := os.Open(config.Datadir+"/icon/"+icon+".png")
		if err != nil {
			serverError(w, err)
			return
		}
		defer file.Close()

		b, err := ioutil.ReadAll(file)
		if err != nil {
			serverError(w, err)
			return
		}

		data, err = convert(b, "png", width, height)
		if err != nil {
			serverError(w, err)
			return
		}

		log.Println("Save icon to", filename)
		data_copy := data[:]
		err = ioutil.WriteFile(filename, data_copy, 0777)
		if err != nil {
			serverError(w, err)
			return
		}
	} else if err != nil {
		log.Println("Unexpected error")
		serverError(w, err)
		return
	} else {
		log.Println("Load icon from", filename)
		data, err = ioutil.ReadFile(filename)
		if err != nil {
			serverError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func imageHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}

	vars := mux.Vars(r)
	image := vars["image"]

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE image = ?", image,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	} else if err != nil {
		serverError(w, err)
		return
	}

	if entry.PublishLevel == 0 {
		// publish_level == 0 はentryの所有者しか見えない
		if user != nil && entry.User == user.Id {
			// ok
		} else {
			notFound(w)
			return
		}
	} else if entry.PublishLevel == 1 {
		// publish_level == 1 はentryの所有者かfollowerしか見えない
		if user != nil && entry.User == user.Id {
			// ok
		} else if user != nil {
			followMap := FollowMap{}
			err = dbConn.QueryRow(
				"SELECT user, target, created_at FROM follow_map WHERE user = ? AND target = ?",
				user.Id, entry.User,
			).Scan(
				&followMap.User, &followMap.Target, &followMap.CreatedAt,
			)
			if err == sql.ErrNoRows {
				notFound(w)
				return
			} else if err != nil {
				serverError(w, err)
				return
			}
		} else {
			notFound(w)
			return
		}
	}

	size := r.FormValue("size")
	if size == "" {
		size = "l"
	}

	var width, height int
	if size == "s" {
		width = imageS
	} else if size == "m" {
		width = imageM
	} else if size == "l" {
		width = imageL
	} else {
		width = imageL
	}
	log.Println("size: " + size)

	filename := "/home/isucon/static/image/" + size + "/" + image + ".jpg"
	var data []byte

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		var data []byte
		if 0 <= width {
			file, err := os.Open(config.Datadir+"/image/"+image+".jpg")
			if err != nil {
				serverError(w, err)
				return
			}
			defer file.Close()

			image, _, err := imagepkg.Decode(file)
			data2, err := cropSquare(image, "jpg")
			if err != nil {
				serverError(w, err)
				return
			}
			b, err := convert(data2, "jpg", width, height)
			if err != nil {
				serverError(w, err)
				return
			}
			data = b
		} else {
			b, err := ioutil.ReadFile(config.Datadir + "/image/" + image + ".jpg")
			if err != nil {
				serverError(w, err)
				return
			}
			data = b
		}

		log.Println("Save image to", filename)
		data_copy := data[:]
		err := ioutil.WriteFile(filename, data_copy, 0777)
		if err != nil {
			log.Println("Failed to write file", filename)
			serverError(w, err)
			return
		}
	} else if err != nil {
		log.Println("Unexpected err")
		serverError(w, err)
		return
	} else {
		log.Println("Load image from", filename)
		data, err = ioutil.ReadFile(filename)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(data)
}

func deleteEntryHandler(w http.ResponseWriter, r *http.Request) {
	prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	vars := mux.Vars(r)
	id := vars["id"]
	method := r.FormValue("__method")

	entry := Entry{}
	err = dbConn.QueryRow(
		"SELECT * FROM entries WHERE id = ?", id,
	).Scan(
		&entry.Id, &entry.User, &entry.Image, &entry.PublishLevel, &entry.CreatedAt,
	)
	if err == sql.ErrNoRows {
		notFound(w)
		return
	} else if err != nil {
		serverError(w, err)
		return
	}

	if user.Id != entry.User || method != "DELETE" {
		badRequest(w)
		return
	}

	_, err = dbConn.Exec("DELETE FROM entries WHERE id = ?", entry.Id)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{"ok": true})
}

func getFollowing(w http.ResponseWriter, user *User, baseUrl *url.URL) {
	rows, err := dbConn.Query(
		"SELECT users.* FROM follow_map JOIN users ON (follow_map.target = users.id) WHERE follow_map.user = ? ORDER BY follow_map.created_at DESC",
		user.Id,
	)
	if err != nil {
		serverError(w, err)
		return
	}
	res := []Response{}
	for rows.Next() {
		u := User{}
		rows.Scan(&u.Id, &u.Name, &u.Apikey, &u.Icon)
		res = append(res, Response{
			"id":   u.Id,
			"name": u.Name,
			"icon": baseUrl.String() + "/icon/" + u.Icon,
		})
	}
	rows.Close()

	renderJsonNoCache(w, Response{"users": res})
}

func followingHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	getFollowing(w, user, baseUrl)
}

func followHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	if err := r.ParseForm(); err != nil {
		serverError(w, err)
		return
	}

	for _, targetStr := range r.Form["target"] {
		target, _ := strconv.Atoi(targetStr)
		if user.Id == target {
			continue
		}
		_, err := dbConn.Exec(
			"INSERT IGNORE INTO follow_map (user, target, created_at) VALUES (?, ?, NOW())",
			user.Id, target,
		)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	getFollowing(w, user, baseUrl)
}

func unfollowHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	if err := r.ParseForm(); err != nil {
		serverError(w, err)
	}

	for _, targetStr := range r.Form["target"] {
		target, _ := strconv.Atoi(targetStr)
		if user.Id == target {
			continue
		}
		_, err := dbConn.Exec(
			"DELETE FROM follow_map WHERE user = ? AND target = ?",
			user.Id, target,
		)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	getFollowing(w, user, baseUrl)
}

func updateIconHandler(w http.ResponseWriter, r *http.Request) {
	baseUrl := prepareHandler(w, r)

	user, err := getUser(r)
	if err != nil {
		serverError(w, err)
		return
	}
	if user == nil {
		badRequest(w)
		return
	}

	uploadFile, handler, err := r.FormFile("image")
	if err != nil {
		serverError(w, err)
		return
	}
	if handler == nil {
		badRequest(w)
		return
	}

	contentType := handler.Header.Get("Content-Type")
	if !(contentType == "image/jpeg" || contentType == "image/jpg" || contentType == "image/png") {
		badRequest(w)
		return
	}

	image, _, err := imagepkg.Decode(uploadFile)
	if err != nil {
		serverError(w, err)
		return
	}
	data2, err := cropSquare(image, "png")
	if err != nil {
		serverError(w, err)
		return
	}

	iconId := sha256Hex(uuid.NewUUID())
	err = ioutil.WriteFile(config.Datadir+"/icon/"+iconId+".png", data2, 0666)
	if err != nil {
		serverError(w, err)
		return
	}

	_, err = dbConn.Exec(
		"UPDATE users SET icon = ? WHERE id = ?",
		iconId, user.Id,
	)
	if err != nil {
		serverError(w, err)
		return
	}

	renderJson(w, Response{"icon": baseUrl.String() + "/icon/" + iconId})
}
