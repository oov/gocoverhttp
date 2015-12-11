package main

import (
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"gopkg.in/fsnotify.v1"
)

var (
	mu     sync.RWMutex
	html   []byte
	stdout = make([]byte, 0)
	stderr = make([]byte, 0)
)

func run() error {
	f, err := ioutil.TempFile(os.TempDir(), "gocoverauto")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	params := []string{"test", "-x", "-v", "-coverprofile", f.Name()}
	params = append(params, os.Args[1:]...)
	cmd := exec.Command("go", params...)
	log.Println("RUN:", cmd.Args)
	sout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer sout.Close()
	serr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	defer serr.Close()
	if err = cmd.Start(); err != nil {
		return err
	}
	mu.Lock()
	html = nil
	stdout = stdout[:0]
	stderr = stderr[:0]
	mu.Unlock()
	go func() {
		var buf [128]byte
		for {
			n, err := sout.Read(buf[:])
			mu.Lock()
			stdout = append(stdout, buf[:n]...)
			mu.Unlock()
			if err != nil {
				if err != io.EOF {
					log.Println("StdOutReader:", err)
				}
				return
			}
		}
	}()
	go func() {
		var buf [128]byte
		for {
			n, err := serr.Read(buf[:])
			mu.Lock()
			stderr = append(stderr, buf[:n]...)
			mu.Unlock()
			if err != nil {
				if err != io.EOF {
					log.Println("StdErrReader:", err)
				}
				return
			}
		}
	}()
	if err = cmd.Wait(); err != nil {
		return err
	}

	hf, err := ioutil.TempFile(os.TempDir(), "gocoverauto")
	if err != nil {
		return err
	}
	defer os.Remove(hf.Name())
	defer hf.Close()
	cmd = exec.Command("go", "tool", "cover", "-html", f.Name(), "-o", hf.Name())
	log.Println("RUN:", cmd.Args)
	if err = cmd.Run(); err != nil {
		return err
	}
	if _, err = hf.Seek(0, os.SEEK_SET); err != nil {
		return err
	}
	buf, err := ioutil.ReadAll(hf)
	if err != nil {
		return err
	}
	mu.Lock()
	html = buf
	mu.Unlock()
	return nil
}

func peek(w http.ResponseWriter) {
	mu.RLock()
	if h := html; h != nil {
		mu.RUnlock()
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Header().Set("X-Progress", "complete")
		w.Write(h)
		return
	}
	if o, e := stdout, stderr; o != nil && e != nil {
		mu.RUnlock()
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.Header().Set("X-Progress", "progress")
		w.Write([]byte(`<!DOCTYPE html><html><h1>StdOut:</h1><pre id="stdout">`))
		template.HTMLEscape(w, o)
		w.Write([]byte(`</pre><h1>StdErr:</h1><pre id="stderr">`))
		template.HTMLEscape(w, e)
		w.Write([]byte(`</pre><script>
function update(){
	var xhr = new XMLHttpRequest();
	xhr.open("GET","/");
	xhr.responseType = "document";
	xhr.send();
	xhr.addEventListener("loadend",function(e){
		if (xhr.getResponseHeader("X-Progress") == "complete") {
			location.reload();
			return;
		}
		var no = xhr.responseXML.getElementById("stdout");
		var ne = xhr.responseXML.getElementById("stderr");
		var oo = document.getElementById("stdout");
		var oe = document.getElementById("stderr");
		oo.parentNode.insertBefore(no,oo);
		oo.parentNode.removeChild(oo);
		oe.parentNode.insertBefore(ne,oe);
		oe.parentNode.removeChild(oe);
		setTimeout(update, 1000);
	});
}
update();
</script></html>`))
		return
	}
	mu.RUnlock()
	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	w.Header().Set("X-Progress", "progress")
	w.Write([]byte(`<!DOCTYPE html><html>processing...<script>setTimeout(function(){location.reload();},1000);</script></html>`))
}

func watch(dir string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println(err)
		return
	}

	if err = w.Add(dir); err != nil {
		log.Println(err)
		return
	}

	do := time.NewTimer(time.Second)
	for {
		select {
		case <-w.Events:
			do.Reset(time.Second)
		case err := <-w.Errors:
			log.Println(err)
		case <-do.C:
			if err := run(); err != nil {
				log.Println(err)
			}
		}
	}
	do.Stop()
	if err = w.Close(); err != nil {
		log.Println(err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	peek(w)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "6066"
	}
	go watch("./")
	http.ListenAndServe(":"+port, http.HandlerFunc(handler))
}
