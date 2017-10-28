package main

import (
	"io/ioutil"
	"os"
	"log"
	"path/filepath"
	imagepkg "image"
	_ "image/jpeg"
	_ "image/png"
	"sync"
)

func main(){
	dir := "./data/image/"
	paths, err := ioutil.ReadDir(dir)
	if err != nil {
		return
	}
	var wg sync.WaitGroup

	for _, path := range paths {
		for _, size := range []string{"s", "m", "l"} {
			wg.Add(1)
			go func(path os.FileInfo, size string) {
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

				filename := "/home/isucon/static/image/" + size + "/" + path.Name()

				if _, err := os.Stat(filename); os.IsNotExist(err) {
					var data []byte
					if 0 <= width {
						file, err := os.Open(filepath.Join(dir, path.Name()))
						if err != nil {
							return
						}
						defer file.Close()

						image, _, err := imagepkg.Decode(file)
						data, err := cropSquare(image, "jpg")
						if err != nil {
							return
						}
						b, err := convert(data, "jpg", width, height)
						if err != nil {
							return
						}
						data = b
					} else {
						b, err := ioutil.ReadFile(filepath.Join(dir, path.Name()))
						if err != nil {
							return
						}
						data = b
					}

					log.Println("Save image to", filename)
					data_copy := data[:]
					err := ioutil.WriteFile(filename, data_copy, 0777)
					if err != nil {
						log.Println("Failed to write file", filename)
						return
					}
				} else if err != nil {
					log.Println("Unexpected err")
					return
				}
				wg.Done()
			}(path, size)
		}
	}
	wg.Wait()
}