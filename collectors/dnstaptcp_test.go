package collectors

import (
	"bufio"
	"log"
	"net"
	"testing"
	"time"

	"github.com/dmachard/go-dnscollector/common"
	"github.com/dmachard/go-framestream"
	"google.golang.org/protobuf/proto"
)

func TestDnstapTcpRun(t *testing.T) {
	g := common.NewFakeGenerator()
	c := NewDnstapTcp([]common.Worker{g}, common.GetFakeConfig(), common.GetFakeLogger(false))
	if err := c.Listen(); err != nil {
		log.Fatal("collector dnstap tcp listening  error: ", err)
	}
	go c.Run()

	conn, err := net.Dial("tcp", ":6000")
	if err != nil {
		t.Error("could not connect to TCP server: ", err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	fs := framestream.NewFstrm(r, w, conn, 5*time.Second, []byte("protobuf:dnstap.Dnstap"), true)
	if err := fs.InitSender(); err != nil {
		t.Fatalf("framestream init error: %s", err)
	} else {
		frame := &framestream.Frame{}

		// get fake dns question
		dnsquery, err := common.GetFakeDns()
		if err != nil {
			t.Fatalf("dns question pack error")
		}

		// get fake dnstap message
		dt_query := common.GetFakeDnstap(dnsquery)

		// serialize to bytes
		data, err := proto.Marshal(dt_query)
		if err != nil {
			t.Fatalf("dnstap proto marshal error %s", err)
		}

		// send query
		frame.Write(data)
		if err := fs.SendFrame(frame); err != nil {
			t.Fatalf("send frame error %s", err)
		}
	}

	// waiting message in channel
	msg := <-g.Channel()
	if msg.Operation != "CLIENT_QUERY" {
		t.Errorf("want CLIENT_QUERY, got %s", msg.Operation)
	}
}