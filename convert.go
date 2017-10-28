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

func Convertfile(){
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
							log.Println("failed to open", err)
							return
						}

						image, _, err := imagepkg.Decode(file)
						if err != nil {
							log.Println("Failed to Decode", err)
							return
						}
						data, err := cropSquare(image, "jpg")
						if err != nil {
							log.Println("Failed to crop", err)
							return
						}
						b, err := convert(data, "jpg", width, height)
						if err != nil {
							log.Println("Failed to convert", err)
							return
						}
						data = b
						file.Close()
					} else {
						b, err := ioutil.ReadFile(filepath.Join(dir, path.Name()))
						if err != nil {
							log.Println("failed to read file", err)
							return
						}
						data = b
					}

					log.Println("Save image to", filename)
					data_copy := data[:]
					err := ioutil.WriteFile(filename, data_copy, 0777)
					if err != nil {
						log.Println("Failed to write file", filename, err)
						return
					}
				} else if err != nil {
					log.Println("Unexpected err", err)
					return
				}
				wg.Done()
			}(path, size)
		}
	}
	wg.Wait()
}