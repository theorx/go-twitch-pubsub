package twitchpubsub

import (
	"errors"
	"log"
	"sync"
	"time"
)

const (
	reconnectInterval      = 5 * time.Second
	pingInterval           = 4 * time.Minute
	pongDeadlineTime       = 9 * time.Second
	writerBufferLength     = 100
	readerBufferLength     = 100
	messageBusBufferLength = 50

	// maximum number of connections to open
	defaultConnectionLimit = 10

	// maximum number of topics one connection can listen to
	defaultTopicLimit = 50
)

var (
	// ErrNotConnected is returned if an action is attempted to be performed on a Client when it is not connected
	ErrNotConnected = errors.New("go-twitch-pubsub: Not connected")

	// ErrDisconnectedByUser is returned from Connect after the user calls Disconnect()
	ErrDisconnectedByUser = errors.New("go-twitch-pubsub: Disconnected by user")

	// DefaultHost is the default host to connect to Twitch's pubsub servers
	DefaultHost = "wss://pubsub-edge.twitch.tv"
)

type messageBusType chan SharedMessage

// Client is the client that connects to Twitch's pubsub servers
type Client struct {
	// Callbacks
	onModerationAction func(channelID string, data *ModerationAction)
	onBitsEvent        func(channelID string, data *BitsEvent)

	connectionManager *connectionManager

	topics *topicManager

	messageBus chan SharedMessage

	quitChannel   chan struct{}
	customHandler CustomHandler
}

type CustomHandler func(SharedMessage)

// NewClient creates a client struct and fills it in with some default values
func NewClient(host string, customHandler CustomHandler) *Client {
	c := &Client{
		messageBus:    make(chan SharedMessage, messageBusBufferLength),
		quitChannel:   make(chan struct{}),
		customHandler: customHandler,
		topics:        newTopicManager(),
	}

	c.connectionManager = &connectionManager{
		host: host,

		connectionLimit:      defaultConnectionLimit,
		connectionLimitMutex: &sync.RWMutex{},

		topicLimit:      defaultTopicLimit,
		topicLimitMutex: &sync.RWMutex{},

		messageBus:  c.messageBus,
		quitChannel: c.quitChannel,
	}

	return c
}

func (c *Client) SetConnectionLimit(connectionLimit int) {
	c.connectionManager.setConnectionLimit(connectionLimit)
}

func (c *Client) SetTopicLimit(topicLimit int) {
	c.connectionManager.setTopicLimit(topicLimit)
}

// OnModerationAction attaches the given callback to the moderation action event
func (c *Client) OnModerationAction(callback func(channelID string, data *ModerationAction)) {
	c.onModerationAction = callback
}

// OnBitsEvent attaches the given callback to the bits event
func (c *Client) OnBitsEvent(callback func(channelID string, data *BitsEvent)) {
	c.onBitsEvent = callback
}

// Connect starts attempting to connect to the pubsub host
func (c *Client) Start() error {
	go c.connectionManager.run()

	for {
		select {
		case msg := <-c.messageBus:
			switch msg.Message.(type) {
			case *ModerationAction:
				d := msg.Message.(*ModerationAction)
				channelID, err := parseChannelIDFromModerationTopic(msg.Topic)
				if err != nil {
					log.Println("Error parsing channel id from moderation topic:", err)
					continue
				}
				c.onModerationAction(channelID, d)
			case *BitsEvent:
				d := msg.Message.(*BitsEvent)
				channelID, err := parseChannelIDFromBitsTopic(msg.Topic)
				if err != nil {
					log.Println("Error parsing channel id from bits topic:", err)
					continue
				}
				c.onBitsEvent(channelID, d)
			default:
				if c.customHandler != nil {
					c.customHandler(msg)
					continue
				}
				log.Println("unknown message in message bus")
			}
		case <-c.quitChannel:
			return ErrDisconnectedByUser
		}
	}
}

// Disconnect disconnects from Twitch's pubsub servers and leave the client in an idle state
func (c *Client) Disconnect() {
	c.connectionManager.disconnect()

	close(c.quitChannel)
}

// Listen sends a message to Twitch's pubsub servers telling them we're interested in a specific topic
// Some topics require authentication, and for those you will need to pass a valid authentication token
func (c *Client) Listen(topicName string, authToken string) {
	topic := newTopic(topicName, authToken)

	if !c.topics.Add(topic) {
		// We were already subscribed to this topic
		return
	}

	c.connectionManager.refreshTopic(topic)
}
