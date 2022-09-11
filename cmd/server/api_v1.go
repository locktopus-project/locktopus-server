package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	ns "github.com/xshkut/gearlock/internal/namespace"
	ml "github.com/xshkut/gearlock/pkg/multilocker"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

const invalidInputCode = 3000

func apiV1Handler(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")

	if namespace == "" {
		w.Write([]byte("URL parameter 'namespace' is required"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		apiLogger.Error(fmt.Errorf("upgrade error: %w", err))
		return
	}
	defer conn.Close()

	connID := atomic.AddInt64(&lastConnID, 1)

	apiLogger.Infof("New connection from %s [id = %d]", conn.RemoteAddr(), connID)

	ns, created := ns.GetNamespace(namespace)
	if created {
		mainLogger.Infof("Created new multilocker namespace %s", namespace)
	}

	err = handleCommunication(conn, ns, connID)

	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Errorf("communication error: %w", err).Error()))
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(invalidInputCode, ""), time.Now().Add(time.Second))

		apiLogger.Infof("Connection closed [id = %d]: %s", connID, err.Error())

		return
	}

	apiLogger.Infof("Closing connection [id = %d]: %s", connID, err)

	conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
}

type action string

const (
	actionLock    action = "lock"
	actionRelease action = "release"
)

type requestMessage struct {
	Action    action     `json:"action"`
	Resources []resource `json:"resources,omitempty"`
}

type resource struct {
	T    string   `json:"type"`
	Path []string `json:"path"`
}

type responseMessage struct {
	ID     string `json:"id"`
	Action action `json:"action"`
	State  string `json:"state"`
}

type ClientState int

const (
	clientStateEnqueued ClientState = iota
	clientStateAcquired
	clientStateReady
)

var states = [3]string{"enqueued", "acquired", "ready"}

func (cs ClientState) String() string {
	return states[cs]
}

func readMessages(conn *websocket.Conn, ch chan<- requestMessage) (err error) {
	cm := requestMessage{}

	for {
		if err = conn.ReadJSON(&cm); err != nil {
			break
		}

		ch <- cm
	}

	return err
}

func handleCommunication(conn *websocket.Conn, ls *ml.MultiLocker, connID int64) (err error) {
	var readErr error
	var l *ml.Lock
	var id int64
	state := clientStateReady
	ch := make(chan requestMessage)

	go func() {
		readErr = readMessages(conn, ch)
		close(ch)
	}()

	var resourceLocks []ml.ResourceLock

	for {
		incm := requestMessage{}
		opened := true
		received := false

		if state == clientStateEnqueued {
			select {
			case <-l.Ready():
				state = clientStateAcquired

				if err != conn.WriteJSON(responseMessage{ID: fmt.Sprintf("%d", l.ID()), Action: actionLock, State: state.String()}) {
					err = fmt.Errorf("cannot send JSON message: %w", err)
				}

			case incm, opened = <-ch:
				received = true
			}
		}

		if !received {
			incm, opened = <-ch
			if !opened {
				break
			}
		}

		if !opened || err != nil || readErr != nil {
			break
		}

		if err = assertCorrectAction(incm.Action, state); err != nil {
			break
		}

		if incm.Action == actionLock {
			resourceLocks, err = makeResourceLocks(incm.Resources)
			if err != nil {
				break
			}

			lockLogger.Infof("Locking resources for connection [id = %d]: %v...", connID, resourceLocks)

			newLock := ls.Lock(resourceLocks)

			lockLogger.Infof("Locked resources for connection [id = %d]: %v", connID, resourceLocks)

			l = &newLock
			id = l.ID()

			select {
			case <-l.Ready():
				state = clientStateAcquired
			default:
				state = clientStateEnqueued
			}

			if err != conn.WriteJSON(responseMessage{ID: fmt.Sprintf("%d", id), Action: incm.Action, State: state.String()}) {
				err = fmt.Errorf("cannot send JSON message: %w", err)
				break
			}

			continue
		}

		go func(l *ml.Lock) {
			l.Acquire().Unlock()
		}(l)

		l = nil

		lockLogger.Infof("Released resources for connection [id = %d]: %v", connID, resourceLocks)

		state = clientStateReady

		if err != conn.WriteJSON(responseMessage{ID: fmt.Sprintf("%d", id), Action: incm.Action, State: state.String()}) {
			err = fmt.Errorf("cannot send JSON message: %w", err)
			break
		}

	}

	if l != nil {
		l.Acquire().Unlock()
	}

	if readErr != nil {
		err = readErr
	}

	return err
}

func parseLockType(input string) (ml.LockType, error) {
	lt := ml.LockTypeRead

	switch strings.ToLower(input) {
	case "r":
		lt = ml.LockTypeRead
	case "read":
		lt = ml.LockTypeRead
	case "w":
		lt = ml.LockTypeWrite
	case "write":
		lt = ml.LockTypeWrite
	default:
		return ml.LockTypeRead, fmt.Errorf("invalid lock type: %s", input)
	}

	return lt, nil
}

func makeResourceLocks(resources []resource) ([]ml.ResourceLock, error) {
	resourceLocks := make([]ml.ResourceLock, len(resources))

	for i, r := range resources {
		lt, err := parseLockType(r.T)
		if err != nil {
			return nil, fmt.Errorf("cannot build resource lock: %w", err)
		}

		resourceLocks[i] = ml.NewResourceLock(lt, r.Path)
	}

	return resourceLocks, nil
}

func assertCorrectAction(action action, state ClientState) error {
	if action != actionLock && action != actionRelease {
		return fmt.Errorf("invalid action: %s", action)
	}

	if action == actionLock && state == clientStateReady {
		return nil
	}

	if action == actionRelease && state != clientStateReady {
		return nil
	}

	return fmt.Errorf("invalid action [%s] in state [%s]", action, state)
}
