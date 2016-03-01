package clients

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/nanopack/mist/core"
	"github.com/nanopack/mist/subscription"
	"github.com/nanopack/mist/util"
)

//
type (

	// A tcpClient represents a TCP connection to the mist server
	tcpClient struct {
		sync.Mutex

		address       string
		subscriptions subscription.Subscriptions // local copy of subscriptions
		conn          net.Conn                   // the connection the mist server
		done          chan error                 // the channel to indicate that the connection is closed
		waiting       []chan tcpReply            // all client waiting for a response
		messages      chan mist.Message          // the channel that mist server 'publishes' updates to
		open          bool                       // flag that indicates that the conenction should reestablish
		attempts      int
		replicated    bool // is replication enabled on this connection
	}

	tcpReply struct {
		value interface{}
		err   error
	}
)

// Connect attempts to connect to a running mist server at the clients specified
// host and port.
func NewTCP(address string) (mist.Client, error) {
	fmt.Println("NEW TCPC!")

	client := &tcpClient{
		subscriptions: subscription.NewNode(),
		done:          make(chan error),
		waiting:       make([]chan tcpReply, 0),
		messages:      make(chan mist.Message),
		open:          false,
		attempts:      0,
		address:       address,
	}

	return client, client.connect()
}

// connect
func (client *tcpClient) connect() error {

	fmt.Println("TCPC CONNECT!")

	// attempt an initial connection to the server
	conn, err := net.Dial("tcp", client.address)
	if err != nil {
		return err
	}

	client.conn = conn
	client.open = true

	// keep the connection open
	fmt.Println("START LISTEN!")
	go client.listen()

	return nil
}

//
func (client *tcpClient) reconnect() {

	fmt.Println("TCPC RECONNECT!")

	// attempt to reconnect
	conn, err := net.Dial("tcp", client.address)

	//
	switch {

	//
	case err != nil && client.attempts < 10:
		fmt.Printf("connection failed... attempting to reconnect %v/10\r", client.attempts)
		<-time.After(time.Second)
		client.attempts += 1
		client.reconnect()

	//
	case err != nil:
		fmt.Printf("unable to connect to server...")

	//
	default:
		fmt.Println("DEFAULT??")
		client.conn = conn
		client.open = true

		//
		go client.listen()
	}
}

//
func (client *tcpClient) listen() {

	fmt.Println("TCPC listen!", client.open)

	//
	for client.open {

		// reenable replication
		// if client.replicated {
		// 	client.async("enable-replication\n")
		// }

		// send all saved subscriptions across the channel
		// client.Lock()
		// for _, subscription := range client.subscriptions.ToSlice() {
		// 	client.async("subscribe %v\n", strings.Join(subscription, ","))
		// }
		// client.Unlock()

		r := util.NewReader(client.conn)
		for r.Next() {

			//
			cmd := r.Input.Cmd

			//
			args := r.Input.Args

			//
			switch cmd {

			//
			case "pong":
				fmt.Println("TCPC listen pong")
				client.Lock()
				wait := client.waiting[0]
				client.waiting = client.waiting[1:]
				client.Unlock()

				//
				wait <- tcpReply{"pong", nil}

			//
			case "publish":
				fmt.Println("TCPC listen publish?")
				if len(args) != 2 {
					// too few args; unable to publish
				}

				//
				select {
				case client.messages <- mist.Message{Tags: strings.Split(args[0], ","), Data: args[1]}:
				case <-client.done:
					fmt.Println("DONE?")
				}

			//
			case "list":
				fmt.Println("TCPC listen list")
				client.Lock()
				wait := client.waiting[0]
				client.waiting = client.waiting[1:]
				client.Unlock()

				list := [][]string{strings.Split(args[1], ",")}

				// if len(cmd) == 3 {
				// 	for _, subscription := range strings.Split(args[2], " ") {
				// 		list = append(list, strings.Split(subscription, ","))
				// 	}
				// }

				//
				wait <- tcpReply{list, nil}

			//
			case "error":
				fmt.Println("TCPC listen error")

				// close the connection as something is seriously wrong, it will reconnect
				// and and continue on
				client.conn.Close()

				waiting := make([]chan tcpReply, 0)

				client.Lock()
				waiting, client.waiting = client.waiting, waiting
				client.Unlock()

				for _, wait := range waiting {
					wait <- tcpReply{"", fmt.Errorf("%v", cmd[0])}
				}

			//
			default:

			}
		}
	}

	// if the client ever disconnects, attempt to reconnect; may want to put in a
	// limit here so that it doesn't try and connect forever...
	if !client.open {
		fmt.Println("RECONNECTING??", client.open)
		client.reconnect()
	}
}

// Ping pong the server
func (client *tcpClient) Ping() error {
	fmt.Println("TCPC PING!")

	return client.sync("ping\n").err
}

// Subscribe takes the specified tags and tells the server to subscribe to updates
// on those tags, returning the tags and an error or nil
func (client *tcpClient) Subscribe(tags []string) error {

	fmt.Println("TCPC SUBSCRIBE!", tags)

	client.Lock()
	active := client.subscriptions.Match(tags)
	client.subscriptions.Add(tags)
	client.Unlock()
	if len(tags) == 0 {
		return nil
	}
	if !active {
		return client.async("subscribe %v\n", strings.Join(tags, ","))
	}
	return nil
}

// Unsubscribe takes the specified tags and tells the server to unsubscribe from
// updates on those tags, returning an error or nil
func (client *tcpClient) Unsubscribe(tags []string) error {

	fmt.Println("TCPC UNSUBSCRIBE!", tags)

	client.Lock()
	client.subscriptions.Remove(tags)
	active := client.subscriptions.Match(tags)
	client.Unlock()
	if len(tags) == 0 {
		return nil
	}
	if !active {
		return client.async("unsubscribe %v\n", strings.Join(tags, ","))
	}
	return nil
}

// Publish sends a message to the mist server to be published to all subscribed clients
func (client *tcpClient) Publish(tags []string, data string) error {
	fmt.Println("TCPC PUBLISH!", tags)
	if len(tags) == 0 {
		return nil
	}
	return client.async("publish %v %v\n", strings.Join(tags, ","), data)
}

// PublishAfter sends a message to the mist server to be published to all subscribed clients
// with delay
func (client *tcpClient) PublishAfter(tags []string, data string, delay time.Duration) error {
	fmt.Println("TCPC PUBLISH AFTER!", tags)
	go func() {
		<-time.After(delay)
		client.Publish(tags, data)
	}()
	return nil
}

// List requests a list of current mist subscriptions from the server
func (client *tcpClient) List() ([][]string, error) {

	fmt.Println("TCPC LIST!")

	//
	reply := client.sync("list\n")

	if reply.value == nil {
		return nil, reply.err
	}

	return reply.value.([][]string), reply.err
}

// Close closes the client data channel and the connection to the server
func (client *tcpClient) Close() error {

	fmt.Println("TCPC CLOSE!")

	// we need to do it in this order in case the goroutine is stuck waiting for
	// more data from the socket
	client.conn.Close()
	close(client.done)
	client.open = false

	return nil
}

//
func (client *tcpClient) Messages() <-chan mist.Message {
	fmt.Println("TCPC MESSAGE?")
	return client.messages
}

func (client *tcpClient) EnableReplication() error {
	client.replicated = true
	return client.async("enable-replication\n")
}

//
func (client *tcpClient) sync(command string) tcpReply {
	wait := make(chan tcpReply, 1)

	client.Lock()
	if _, err := fmt.Fprintf(client.conn, command); err != nil {
		client.Unlock()
		return tcpReply{nil, err}
	}
	client.waiting = append(client.waiting, wait)
	client.Unlock()

	return <-wait
}

//
func (client *tcpClient) async(format string, args ...interface{}) error {
	_, err := fmt.Fprintf(client.conn, format, args...)
	return err
}
