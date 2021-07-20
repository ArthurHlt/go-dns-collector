package dnsmessage

import (
	"hash/fnv"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/dmachard/go-dnstap-protobuf"
	"github.com/dmachard/go-logger"
	"google.golang.org/protobuf/proto"
)

/*

dnstap decoder from one channel to dnsmessage in N channels

                                                 |---> channel 1 (dnsmessage)
dnstap --> channel in -> --- (dnstapdecoder)-----|---> channel 2
                                                 |---> channel n

*/

type DnstapConsumer struct {
	done      chan bool
	recv_from chan []byte
	logger    *logger.Logger
}

func NewDnstapConsumer(logger *logger.Logger) DnstapConsumer {
	logger.Info("new dnstap consumer")
	d := DnstapConsumer{
		done:      make(chan bool),
		recv_from: make(chan []byte, 512),
		logger:    logger,
	}
	return d
}

func (d *DnstapConsumer) GetChannel() chan []byte {
	return d.recv_from
}

func (d *DnstapConsumer) Stop() {
	close(d.recv_from)

	// read done channel and block until run is terminated
	<-d.done
	close(d.done)
}

func (d *DnstapConsumer) Run(send_to []chan DnsMessage) {

	dt := &dnstap.Dnstap{}
	cache_ttl := NewCacheDns(10 * time.Second)

	for data := range d.recv_from {

		err := proto.Unmarshal(data, dt)
		if err != nil {
			continue
		}

		dm := DnsMessage{}
		dm.Init()

		identity := dt.GetIdentity()
		if len(identity) > 0 {
			dm.Identity = string(identity)
		}

		dm.Operation = dt.GetMessage().GetType().String()
		dm.Family = dt.GetMessage().GetSocketFamily().String()
		dm.Protocol = dt.GetMessage().GetSocketProtocol().String()

		// decode query address and port
		queryip := dt.GetMessage().GetQueryAddress()
		if len(queryip) > 0 {
			dm.QueryIp = net.IP(queryip).String()
		}
		queryport := dt.GetMessage().GetQueryPort()
		if queryport > 0 {
			dm.QueryPort = strconv.FormatUint(uint64(queryport), 10)
		}

		// decode response address and port
		responseip := dt.GetMessage().GetResponseAddress()
		if len(responseip) > 0 {
			dm.ResponseIp = net.IP(responseip).String()
		}
		responseport := dt.GetMessage().GetResponsePort()
		if responseport > 0 {
			dm.ResponsePort = strconv.FormatUint(uint64(responseport), 10)
		}

		// get dns payload and timestamp according to the type (query or response)
		op := dnstap.Message_Type_value[dm.Operation]
		if op%2 == 1 {
			dns_payload := dt.GetMessage().GetQueryMessage()
			dm.Payload = dns_payload
			dm.Length = len(dns_payload)
			dm.Type = "query"
			dm.Timesec = int(dt.GetMessage().GetQueryTimeSec())
			dm.Timensec = int(dt.GetMessage().GetQueryTimeNsec())
		} else {
			dns_payload := dt.GetMessage().GetResponseMessage()
			dm.Payload = dns_payload
			dm.Length = len(dns_payload)
			dm.Type = "reply"
			dm.Timesec = int(dt.GetMessage().GetResponseTimeSec())
			dm.Timensec = int(dt.GetMessage().GetResponseTimeNsec())
		}

		// compute timestamp
		dm.Timestamp = float64(dm.Timesec) + float64(dm.Timensec)/1e9

		// decode the dns payload to get id, rcode and the number of question
		// ignore invalid packet
		dns_id, dns_rcode, dns_qdcount, err := DecodeDns(dm.Payload)
		if err != nil {
			d.logger.Error("dnstap parser error: %s", err)
			continue
		}

		dm.Id = dns_id
		dm.Rcode = RcodeToString(dns_rcode)

		// continue to decode the dns payload to extract the qname and rrtype
		if dns_qdcount > 0 {
			dns_qname, dns_rrtype := DecodeQuestion(dm.Payload)
			dm.Qname = dns_qname
			dm.Qtype = RdatatypeToString(dns_rrtype)
		}

		// compute latency if possible
		if len(queryip) > 0 && queryport > 0 {
			// compute the hash of the query
			hash_data := []string{dm.QueryIp, dm.QueryPort, strconv.Itoa(dm.Id)}

			hashfnv := fnv.New64a()
			hashfnv.Write([]byte(strings.Join(hash_data[:], "+")))

			if dm.Type == "query" {
				cache_ttl.Set(hashfnv.Sum64(), dm.Timestamp)
			} else {
				value, ok := cache_ttl.Get(hashfnv.Sum64())
				if ok {
					dm.Latency = dm.Timestamp - value
				}
			}
		}

		for i := range send_to {
			send_to[i] <- dm
		}
	}

	// dnstap channel consumer closed
	d.done <- true
}