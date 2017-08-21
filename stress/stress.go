package stress

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"stress/stress/proxyclient"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"golang.org/x/net/http2"
)

type (
	//Task contains the stress test configuration and request configuration.
	Task struct {
		// Request is the request to be made.
		Request *http.Request
		//ReqBody is the request of body.
		ReqBody []byte
		//Nuber is the total number of requests to send.
		Number int
		//Concurrent is the concurrent number of requests.
		Concurrent int
		Output     string
		//Timeout is the timeout of request in seconds.
		Timeout int
		//ThinkTime is the think time of request in seconds.
		ThinkTime int
		// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
		ProxyAddr   *url.URL
		Socket5List []*Socket5
		TCPData     [][]byte
		TCPAddr     string
		TCPInterval int
		//H2 is an option to make HTTP/2 requests.
		H2 bool
		//DisableCompression is an option to disable compression in response.
		DisableCompression bool
		//DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests.
		DisableKeepAlives bool
		//DisableRedirects is an option to prevent the following of HTTP redirects.
		DisableRedirects bool

		//The total think time required for all requests.
		thinkDuration time.Duration
		start         time.Time
		results       chan *Result
	}
)

type Socket5 struct {
	Socket5Type string
	Socket5Addr string
	Socket5Auth *proxy.Auth
}

//Run is run a task.
func (t *Task) Run() {
	if t.Number > 0 && t.TCPAddr == "" {
		t.results = make(chan *Result, t.Number)
	}
	t.start = time.Now()
	t.runRequesters()
	t.finish()
}

func (t *Task) finish() {
	if t.TCPAddr == "" {
		close(t.results)
		if t.Number < 0 {
			return
		}
		total := time.Now().Sub(t.start) - t.thinkDuration
		newReport(os.Stdout, t.results, t.Output, total).finalize()
	}
}

func (t *Task) runRequesters() {
	dlen := len(t.Socket5List)
	if dlen > 0 {
		t.Concurrent *= dlen
	}
	var wg sync.WaitGroup
	wg.Add(t.Concurrent)
	for i, j := 0, 0; i < t.Concurrent; i++ {
		var socketConfig *Socket5
		if dlen > 0 {
			socketConfig = t.Socket5List[j]
			j++
			if j >= dlen {
				j = 0
			}
		}
		if t.TCPAddr != "" {
			go func() {
				t.runTCPRequester(t.Number/t.Concurrent, socketConfig)
				wg.Done()
			}()
		} else {
			go func() {
				t.runRequester(t.Number/t.Concurrent, socketConfig)
				wg.Done()
			}()
		}
	}
	wg.Wait()
}

func (t *Task) runRequester(num int, socketConfig *Socket5) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression: t.DisableCompression,
		DisableKeepAlives:  t.DisableKeepAlives,
		Proxy:              http.ProxyURL(t.ProxyAddr),
	}
	if socketConfig != nil {
		dialer, err := proxy.SOCKS5(socketConfig.Socket5Type, socketConfig.Socket5Addr, socketConfig.Socket5Auth, proxy.Direct)
		if err != nil {
			fmt.Println(err)
		} else {
			transport.Dial = dialer.Dial
		}
	}
	if t.H2 {
		http2.ConfigureTransport(transport)
	} else {
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(t.Timeout) * time.Second,
	}
	if t.DisableRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	if t.Number < 0 {
		for {
			t.sendRequest(client)
		}
	} else {
		for i := 0; i < num; i++ {
			t.sendRequest(client)
		}
	}
}

func (t *Task) sendRequest(client *http.Client) {
	var thinkDuration time.Duration
	start := time.Now()
	var size int64
	var code int
	var dnsStart, connStart, reqStart, resStart, delayStart time.Time
	var dnsDuration, connDuration, reqDuration, resDuration, delayDuration time.Duration
	req := cloneRequest(t.Request, t.ReqBody)
	//Create httptrace.
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			dnsStart = time.Now()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			dnsDuration = time.Now().Sub(dnsStart)
		},
		GetConn: func(h string) {
			connStart = time.Now()
		},
		GotConn: func(connInfo httptrace.GotConnInfo) {
			connDuration = time.Now().Sub(connStart)
			reqStart = time.Now()
		},
		WroteRequest: func(w httptrace.WroteRequestInfo) {
			reqDuration = time.Now().Sub(reqStart)
			delayStart = time.Now()
		},
		GotFirstResponseByte: func() {
			delayDuration = time.Now().Sub(delayStart)
			resStart = time.Now()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	res, err := client.Do(req)
	if err == nil {
		size = res.ContentLength
		code = res.StatusCode
		io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}
	nowTime := time.Now()
	resDuration = nowTime.Sub(resStart)
	end := nowTime.Sub(start)
	result := &Result{
		URLStr:        req.URL.String(),
		Method:        req.Method,
		Err:           err,
		StatusCode:    code,
		Duration:      end,
		ConnDuration:  connDuration,
		DNSDuration:   dnsDuration,
		ReqDuration:   reqDuration,
		ResDuration:   resDuration,
		DelayDuration: delayDuration,
		ContentLength: size,
	}
	if t.Number > 0 {
		t.results <- result
	}
	//Handle think time.
	thinktime := time.Duration(t.ThinkTime) * time.Second
	time.Sleep(thinktime)
	thinkDuration += thinktime
	t.thinkDuration += thinktime
}

func (t *Task) runTCPRequester(num int, socketConfig *Socket5) {
	if t.Number < 0 {
		for {
			t.sendTCP(socketConfig)
		}
	} else {
		for i := 0; i < num; i++ {
			t.sendTCP(socketConfig)
		}
	}
}

func (t *Task) sendTCP(socketConfig *Socket5) {
	var conn net.Conn
	var err error
	if socketConfig != nil {
		var user, pwd string
		if socketConfig.Socket5Auth != nil {
			user, pwd = socketConfig.Socket5Auth.User, socketConfig.Socket5Auth.Password
		}
		var proxy proxyclient.ProxyClient
		proxy, err = proxyclient.NewSocksProxyClient("socks5", socketConfig.Socket5Addr, user, pwd, nil, nil)
		if err != nil {
			fmt.Println(err)
			return
		}
		conn, err = proxy.Dial("tcp", socketConfig.Socket5Addr)
	} else {
		var tcpAddr *net.TCPAddr
		tcpAddr, err = net.ResolveTCPAddr("tcp4", t.TCPAddr)
		if err != nil {
			fmt.Println(err)
			return
		}
		conn, err = net.DialTCP("tcp", nil, tcpAddr)
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	if conn == nil {
		fmt.Println("conn is nil")
		return
	}
	for _, data := range t.TCPData {
		conn.Write(data)
		time.Sleep(time.Duration(t.TCPInterval) * time.Millisecond)
	}
	conn.Close()
}

func cloneRequest(r *http.Request, body []byte) *http.Request {
	req := new(http.Request)
	*req = *r
	req.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		req.Header[k] = append([]string(nil), s...)
	}
	if len(body) > 0 {
		req.Body = ioutil.NopCloser(bytes.NewReader(body))
	}
	return req
}
