package stress

import (
	"bytes"
	"crypto/tls"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
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
		ProxyAddr *url.URL
		Dialers   []proxy.Dialer
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

//Run is run a task.
func (t *Task) Run() error {
	if t.Number > 0 {
		t.results = make(chan *Result, t.Number)
	}
	t.start = time.Now()
	t.runRequesters()
	t.finish()
	return nil
}

func (t *Task) finish() {
	close(t.results)
	if t.Number < 0 {
		return
	}
	total := time.Now().Sub(t.start) - t.thinkDuration
	newReport(os.Stdout, t.results, t.Output, total).finalize()
}

func (t *Task) runRequesters() {
	dlen := len(t.Dialers)
	if dlen > 0 {
		t.Concurrent *= dlen
	}
	var wg sync.WaitGroup
	wg.Add(t.Concurrent)
	for i, j := 0, 0; i < t.Concurrent; i++ {
		var dialer proxy.Dialer
		if t.Dialers != nil {
			dialer = t.Dialers[j]
			j++
			if j >= dlen {
				j = 0
			}
		}
		go func(dialer proxy.Dialer) {
			t.runRequester(t.Number/t.Concurrent, dialer)
			wg.Done()
		}(dialer)
	}
	wg.Wait()
}

func (t *Task) runRequester(num int, dialer proxy.Dialer) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression: t.DisableCompression,
		DisableKeepAlives:  t.DisableKeepAlives,
		Proxy:              http.ProxyURL(t.ProxyAddr),
	}
	if dialer != nil {
		transport.Dial = dialer.Dial
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

// func (t *Task) checkAndInitConfigs() error {
// 	if t.Number == 0 && t.Duration <= 0 {
// 		return errors.New("Number or Duration cannot be smaller than 1")
// 	}
// 	if t.Number != 0 && t.Duration > 0 {
// 		return errors.New("Number and Duration only set one")
// 	}
// 	if t.Concurrent <= 0 {
// 		return errors.New("Concurrent cannot be smaller than 1")
// 	}
// 	if t.Number > 0 && t.Number < t.Concurrent {
// 		return errors.New("Number cannot be less than Concurrent")
// 	}
// 	if t.Number > 0 && t.Number%t.Concurrent != 0 {
// 		return errors.New("Number must be an integer multiple of Concurrent")
// 	}
// 	if t.Output != "" {
// 		err := os.MkdirAll(t.Output, 0777)
// 		if err != nil {
// 			return err
// 		}
// 	}
// 	if t.Duration <= 0 && t.Number > 0 {
// 		t.results = make([]*Result, 0, t.Number)
// 	}
// 	for i, n := 0, len(t.reqConfigs); i < n; i++ {
// 		if t.reqConfigs[i] == nil {
// 			return errors.New("RequestConfig cannot be nil")
// 		}
// 		if t.reqConfigs[i].URLStr == "" || t.reqConfigs[i].Method == "" {
// 			return errors.New("URLStr and Method cannot be empty")
// 		}
// 		if t.Timeout > 0 && t.reqConfigs[i].Timeout <= 0 {
// 			t.reqConfigs[i].Timeout = t.Timeout
// 		}
// 		if t.ThinkTime > 0 && t.reqConfigs[i].ThinkTime <= 0 {
// 			t.reqConfigs[i].ThinkTime = t.ThinkTime
// 		}
// 		if t.Host != "" && t.reqConfigs[i].Host == "" {
// 			t.reqConfigs[i].Host = t.Host
// 		}
// 		if t.ProxyAddr != nil && t.reqConfigs[i].ProxyAddr == nil {
// 			t.reqConfigs[i].ProxyAddr = t.ProxyAddr
// 		}
// 		if t.DisableCompression && !t.reqConfigs[i].DisableCompression {
// 			t.reqConfigs[i].DisableCompression = true
// 		}
// 		if t.DisableKeepAlives && !t.reqConfigs[i].DisableKeepAlives {
// 			t.reqConfigs[i].DisableKeepAlives = true
// 		}
// 		if t.DisableRedirects && !t.reqConfigs[i].DisableRedirects {
// 			t.reqConfigs[i].DisableRedirects = true
// 		}
// 		t.reqConfigs[i].Method = strings.ToUpper(t.reqConfigs[i].Method)
// 		req, err := http.NewRequest(t.reqConfigs[i].Method, t.reqConfigs[i].URLStr, nil)
// 		if err != nil {
// 			return err
// 		}
// 		if t.reqConfigs[i].Header != nil {
// 			req.Header = t.reqConfigs[i].Header
// 		}
// 		t.reqConfigs[i].request = req
// 	}

// 	return nil
// }