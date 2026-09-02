package main

import (
	"bufio"
	"fmt"
	"github.com/mo2iairi/airipress/internal/api"
	"github.com/mo2iairi/airipress/internal/store"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] != "hash-password" || len(os.Args) != 2 {
			log.Fatal("usage: airipress [hash-password]")
		}
		hashPassword()
		return
	}
	dsn := os.Getenv("AIRIPRESS_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "file:.meta/airipress.db?cache=shared"
	}
	dsn = normalizeDatabaseDSN(dsn)
	if strings.HasPrefix(dsn, "file:") {
		path := strings.TrimPrefix(dsn, "file:")
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		if path != ":memory:" && path != "" {
			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				log.Fatal(err)
			}
			old := filepath.Join(dir, "..", "airipress.db")
			if filepath.Clean(path) != filepath.Clean(old) {
				_, newErr := os.Stat(path)
				_, oldErr := os.Stat(old)
				if os.IsNotExist(newErr) && oldErr == nil {
					if err := os.Rename(old, path); err != nil {
						log.Fatal(err)
					}
					for _, suffix := range []string{"-wal", "-shm"} {
						oldComp, newComp := old+suffix, path+suffix
						if _, e := os.Stat(oldComp); e == nil {
							if _, e = os.Stat(newComp); e == nil {
								log.Fatal("both legacy and new database companion files exist")
							}
							if e = os.Rename(oldComp, newComp); e != nil {
								log.Fatal(e)
							}
						}
					}
				} else if newErr == nil && oldErr == nil {
					log.Fatal("both legacy and new database files exist")
				}
			}
		}
	}
	db, err := store.Open(dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	h, err := api.NewChecked(db)
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8787"
	}
	log.Printf("airipress listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, h))
}

func normalizeDatabaseDSN(dsn string) string {
	if !strings.HasPrefix(dsn, "file:") {
		return dsn
	}
	value := strings.TrimPrefix(dsn, "file:")
	query := ""
	if i := strings.IndexByte(value, '?'); i >= 0 {
		query = value[i:]
		value = value[:i]
	}
	switch filepath.Clean(value) {
	case "/data/airipress.db":
		value = "/data/.meta/airipress.db"
	case "airipress.db":
		value = ".meta/airipress.db"
	}
	return "file:" + value + query
}

func hashPassword() {
	r := bufio.NewReader(io.LimitReader(os.Stdin, 74))
	p, e := r.ReadString('\n')
	if e != nil && len(p) == 0 {
		log.Fatal("password must be provided on stdin")
	}
	p = strings.TrimRight(p, "\r\n")
	if len(p) > 72 {
		log.Fatal("password input is too long")
	}
	if len(p) < 12 {
		log.Fatal("password must contain at least 12 characters")
	}
	h, e := bcrypt.GenerateFromPassword([]byte(p), 12)
	if e != nil {
		log.Fatal(e)
	}
	fmt.Println(string(h))
}
