package main

import (
	"bufio"
	"crypto/md5"
	"errors"
	"fmt"
	"github.com/urfave/cli/v2"
	"go-rsubl/rsub"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	AppVersion = "0.1.2"
	Verbose = false
	HelpTemplate = `NAME:
   {{.Name}} - {{.Usage}}
USAGE:
   {{.HelpName}} {{if .VisibleFlags}}[options]{{end}}[file1 file2 ...]}{{if .Commands}}
COMMANDS:
{{range .Commands}}{{if not .HideHelp}}   {{join .Names ", "}}{{ "\t"}}{{.Usage}}{{ "\n" }}{{end}}{{end}}{{end}}{{if .VisibleFlags}}
OPTIONS:
   {{range .VisibleFlags}}{{.}}
   {{end}}{{end}}{{if .Copyright }}
COPYRIGHT:
   {{.Copyright}}
   {{end}}{{if .Version}}
VERSION:
   {{.Version}}
   {{end}}
`

)

var hashes = make(map[string]string)

func sendFile(c *rsub.Conn, path string) error {
	var fileSize int64 = 0
	var fileExist bool = false
	var f *os.File

	fInfo, err := os.Stat(path)
	if err == nil {
		fileExist = true
		fileSize = fInfo.Size()
		if fInfo.IsDir() {
			return errors.New("only file can be edit")
		}


	}

	if !fileExist {
		f, err = os.Create(path)
    } else {
		//open file
		f, err = os.Open(path)
	}
    if err != nil {
    	return err
    }
    defer f.Close()

	displayName := filepath.Base(path)
	hostname, err := os.Hostname()

    if err==nil {
    	displayName = fmt.Sprintf("%s:%s", hostname, displayName)
    }

	realpath, _ := filepath.Abs(path)
	hash := fmt.Sprintf("%x", md5.Sum([]byte(realpath)))

	c.SendString("open\n")
	c.SendString(fmt.Sprintf("display-name: %v\n", displayName))
	c.SendString(fmt.Sprintf("real-path: %v\n", realpath))
	c.SendString("data-on-save: yes\n")
	c.SendString("re-activate: yes\n")
	c.SendString("selection: 0\n")
	c.SendString(fmt.Sprintf("token: %v\n", hash))
	c.SendString(fmt.Sprintf("data: %v\n", fileSize))
        c.SendFile(bufio.NewReader(f))
	c.SendString("\n.\n")
	c.Flush()

	hashes[hash] = path

	return nil
}

func processRecv(c *rsub.Conn, line []byte) (error) {
	cmd := strings.TrimSpace(string(line))
	switch cmd {
	case "close":
		data, err := c.Receive()
		if err!=nil {
			return cli.Exit("connection close and exit", 86)
		}
		line := string(data.([]byte))

		if strings.HasPrefix(line, "token:") {
			token := strings.TrimSpace(line[6:])

			if Verbose {
				log.Printf("close file %s", hashes[token])
			}
			delete(hashes, token)
		}



		return nil
	case "save":
		var token string
		var size int64

		for{
			data, err := c.Receive()
			if err!=nil {
				return cli.Exit("connection close and exit", 86)
			}

			line := string(data.([]byte))
			if strings.HasPrefix(line, "token:") {
				token = strings.TrimSpace(line[6:])
			} else if strings.HasPrefix(line, "data:") {
				size, err = strconv.ParseInt(line[6:], 10, 64)
				if err != nil {
					log.Printf("read data size error")
					return err
				}

				if Verbose {
					log.Printf(" save file token:%s size:%d ", token, size)
				}
				break
			}
		}

		f, err := ioutil.TempFile("", "")
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.CopyN(f, c.GetReader(), int64(size))
		if err != nil {
			return err
		}
		f.Close()

		if file, ok := hashes[token]; ok {
			fi, err := os.Stat(file)
			if err !=nil {
				log.Printf("get file(%s) stat error %s ", file, err)
				return err
			}
			fm := fi.Mode()
			filePerm := fm.Perm()

			err = os.Rename(f.Name(), file)
			if err !=nil {
				log.Printf("save file(%s) failed %s ", file, err)
				return err
			}

			if Verbose {
				log.Printf("save file(%s) %d bytes success", file, size)
			}

			os.Chmod(file, filePerm)
			
			return nil
		}
		return errors.New("save file error token: " + token)
	}
	return nil
}

func main() {
	app := cli.NewApp()
	app.Name = "rsubl"
	app.Usage = "Edit files on a remote server over ssh in Sublime 3"
	app.Version = AppVersion



	//Override a template
	cli.AppHelpTemplate = HelpTemplate
	cli.VersionFlag = &cli.BoolFlag{Name: "version", Value: false, Usage: "print the version"}

	app.Flags = []cli.Flag {
		&cli.StringFlag{Name: "host", Value: "localhost", Usage: "Connect to host."},
		&cli.IntFlag{Name: "port", Value: 52698, Usage: "Port number to use for connection."},
		&cli.BoolFlag{Name: "verbose", Value: false, Usage: "verbose logging messages.", Aliases: []string{"v"}},
	}


	app.Action = func(c *cli.Context) error {

		Verbose = c.Bool("verbose")

		n := c.NArg()
		if n > 0 {

			if !Verbose {

				args := []string{}
				args = append(args, "-v")

				for i:=1; i < len(os.Args); i++ {
					args = append(args, os.Args[i])
				}

				cmd := exec.Command(os.Args[0], args...)
				cmd.Start()

				os.Exit(0)
			}

			//connect to host
			conn, err := rsub.NewConn(c.String("host"), c.Int("port"))
			if err != nil {
				return cli.Exit(fmt.Sprintf("connect %s:%d failed", c.String("host"), c.Int("port")), 86)
			}

			line, err := conn.Receive()
			if (err==nil&&len(line.([]byte))>0) {
				if c.Bool("verbose") {
					log.Printf("connect %s:%d success", c.String("host"), c.Int("port"))
					log.Printf("response: %s", string(line.([]byte)))
				}
			}

			//send file
			for i := 0; i < n; i++ {
				file := c.Args().Get(i)

				err = sendFile(conn, file)
				if err != nil {
					if c.Bool("verbose") {
						log.Printf("send file %s error (%v)", file, err.Error())
					}
				} else {
					if c.Bool("verbose") {
						log.Printf("send file %s success", file)
					}
				}
			}

			//loop
			for{
				if len(hashes)<1 {
					return cli.Exit("exit, no open files", 86)
				}

				line, err := conn.Receive()
				if err!=nil {
					if (err!=io.EOF) {
						return cli.Exit("connection close and exit", 86)
					}
				}

				if line !=nil {
					processRecv(conn, line.([]byte))
				}
			}



		} else {
			cli.ShowAppHelp(c)
		}
		return nil
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
