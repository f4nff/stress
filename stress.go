package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	gurl "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/proxy"

	lbstress "stress/stress"
)

var (
	m        = flag.String("m", "GET", "")
	body     = flag.String("b", "", "")
	bodyFile = flag.String("B", "", "")

	output    = flag.String("o", "", "")
	proxyAddr = flag.String("x", "", "")
	host      = flag.String("host", "", "")

	n             = flag.Int("n", 100, "")
	c             = flag.Int("c", 10, "")
	t             = flag.Int("t", 20, "")
	thinkTime     = flag.Int("think-time", 0, "")
	sendThinkTime = flag.Int("send-think-time", 0, "")

	h2                 = flag.Bool("h2", false, "")
	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepalive   = flag.Bool("disable-keepalive", false, "")
	disableRedirects   = flag.Bool("disable-redirects", false, "")

	socket5     = flag.String("socket5", "", "")
	tcp         = flag.String("tcp", "", "")
	tcpData     = flag.String("tcp-data", "", "")
	tcpInterval = flag.Int("tcp-interval", 50, "")
)

const (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`

	methodsRegexp   = `m:([a-zA-Z]+),*`
	bodyRegexp      = `b:([^,]+),*`
	bodyFileRegexp  = `B:([^,]+),*`
	proxyAddrRegexp = `x:([^,]+),*`
	thinkTimeRegexp = `thinkTime:([\d]+),*`
)

var usage = `Usage: stress [options...] <url> 

Options:
  -n  Number of requests to run. Default value is 100.
      If set to -1, the request has been sent, but the report will 
      not be output by default.
  -c  Number of requests to run concurrently. 
      Total number of requests cannot smaller than the concurrency level. 
      Default value is 10.
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-separated values format.
  
  -h  Custom HTTP header. For example: 
      -h "Accept: text/html" -h "Content-Type: application/xml".
  -m  HTTP method, any of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -t  Timeout for each request in seconds. Default value is 20, 
      use 0 for infinite.
  -b  HTTP request body.
  -B  HTTP request body from file. For example:
      /home/user/file.txt or ./file.txt.
  -x  HTTP Proxy address as host:port.

  -h2 	 Enable HTTP/2.
  -host	 Set HTTP Host header.

  -tcp                  
  -tcp-data
  -tcp-interval
  
  -socket5              Set Socket5 config from file.For example:
                        /home/user/socket5.json or ./socket5.json.
  -think-time           Time to think after request. Default value is 0 sec.
  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                    	connections between different HTTP requests.
  -disable-redirects    Disable following of HTTP redirects.
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
	}
	var hs headerSlice
	flag.Var(&hs, "h", "")
	flag.Parse()
	if *tcp == "" {
		if flag.NArg() <= 0 {
			usageAndExit("")
		}
	}
	num := *n
	conc := *c
	if num == 0 {
		usageAndExit("-n cannot be smaller than 1")
	}
	if conc <= 0 {
		usageAndExit("-c cannot be smaller than 1.")
	}
	if num > 0 && num < conc {
		usageAndExit("-n cannot be less than -c.")
	}
	var socket5s []*lbstress.Socket5
	if *socket5 != "" {
		var err error
		socket5s, err = parseSocket5(*socket5)
		if err != nil {
			errAndExit(err.Error())
		}
	}
	if *tcp != "" {
		tcpAddr, err := net.ResolveTCPAddr("tcp4", *tcp)
		if err != nil {
			errAndExit(err.Error())
		}

		conn, err := net.DialTCP("tcp", nil, tcpAddr)
		if err != nil {
			errAndExit(err.Error())
		}
		conn.Close()
		var datas [][]byte
		if *tcpData != "" {
			var err error
			datas, err = parseFileData(*tcpData)
			if err != nil {
				errAndExit(err.Error())
			}
		}

		task := lbstress.Task{
			TCPData:     datas,
			Number:      *n,
			Concurrent:  *c,
			TCPInterval: *tcpInterval,
			TCPAddr:     *tcp,
			Socket5List: socket5s,
		}
		task.Run()
		return
	}
	url := flag.Args()[0]
	method := strings.ToUpper(*m)

	//Parsing global request header.
	header := make(http.Header)
	for _, h := range hs {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		header.Set(match[1], match[2])
	}

	var bodyAll []byte
	if *body != "" {
		bodyAll = []byte(*body)
	}
	if *bodyFile != "" {
		slurp, err := ioutil.ReadFile(*bodyFile)
		if err != nil {
			errAndExit(err.Error())
		}
		bodyAll = slurp
	}

	if *output != "csv" && *output != "" {
		usageAndExit("Invalid output type; only csv is supported.")
	}

	//Parsing global request proxyAddr.
	var proxyURL *gurl.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = gurl.Parse(*proxyAddr)
		if err != nil {
			usageAndExit(err.Error())
		}
	}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		usageAndExit(err.Error())
	}
	req.Header = header
	// set host header if set
	if *host != "" {
		req.Host = *host
	}
	//Set parameters and global configuration.
	task := &lbstress.Task{
		Request:            req,
		ReqBody:            bodyAll,
		Number:             *n,
		Concurrent:         *c,
		Output:             *output,
		Timeout:            *t,
		ThinkTime:          *thinkTime,
		ProxyAddr:          proxyURL,
		DisableCompression: *disableCompression,
		DisableKeepAlives:  *disableKeepalive,
		DisableRedirects:   *disableRedirects,
		H2:                 *h2,
		Socket5List:        socket5s,
	}
	task.Run()
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

type Socket5Config struct {
	Socket5List []Socket5 `json:"socket5-list"`
}
type Socket5 struct {
	Socket5Type string `json:"socket5-type"`
	Socket5Addr string `json:"socket5-addr"`
	Socket5Auth string `json:"socket5-auth"`
}

func parseSocket5(file string) ([]*lbstress.Socket5, error) {
	var socket5Config Socket5Config
	content, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(content, &socket5Config)
	if err != nil {
		return nil, err
	}
	var configs []*lbstress.Socket5
	for _, config := range socket5Config.Socket5List {
		var socketConfig lbstress.Socket5
		if config.Socket5Auth != "" {
			var username, password string
			match, err := parseInputWithRegexp(config.Socket5Auth, authRegexp)
			if err != nil {
				return nil, err
			}
			username, password = match[1], match[2]

			auth := proxy.Auth{
				User:     username,
				Password: password,
			}
			socketConfig.Socket5Auth = &auth
		}
		socketConfig.Socket5Addr = config.Socket5Addr
		socketConfig.Socket5Type = config.Socket5Type
		configs = append(configs, &socketConfig)
	}
	return configs, nil
}

func parseFileData(file string) ([][]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	var datas [][]byte
	buf := bufio.NewReader(f)
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		line = strings.TrimSpace(line)
		var data []byte
	ESCA:
		if line != "" {
			var val rune
			var err error
			val, _, line, err = strconv.UnquoteChar(line, 0)
			if err != nil {
				return nil, err
			}
			data = append(data, byte(val))
			goto ESCA
		}
		datas = append(datas, data)
	}
	return datas, nil
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg)
		fmt.Fprintf(os.Stderr, "\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func errAndExit(msg string) {
	fmt.Fprintf(os.Stderr, "Error:%s\n", msg)
	os.Exit(1)
}

type headerSlice []string

func (h *headerSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}
