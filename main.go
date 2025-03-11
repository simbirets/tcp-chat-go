package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
)

const (
	password3 = "1234"
)

type Server struct {
	Addr  string
	Ln    net.Listener
	Rooms map[RoomID]*Room
}

func NewServer(addr string) *Server {
	return &Server{
		Addr:  addr,
		Rooms: make(map[RoomID]*Room),
	}
}

func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.Addr)
	s.Ln = ln
	return err
}

type User struct {
	Addr     net.Addr
	RoomId   RoomID
	conn     *net.Conn
	Room     *Room
	UserName string
}

func (u *User) GetConnection() *net.Conn {
	return u.conn
}

func (u *User) GetRoom() *Room {
	return u.Room
}

type RoomID int

type Room struct {
	RoomID   RoomID
	users    []User
	RoomName string
	mu       sync.Mutex
}

func (r *Room) GetUsers() []User {
	return r.users
}

func (r *Room) AddUser(user User) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, roomUser := range r.users {
		if roomUser.Addr.String() == user.Addr.String() {
			return false
		}
	}

	r.users = append(r.users, user)
	return true
}

type ErrorChan struct {
	mu     sync.Mutex
	ErrMap map[string]error
}

func (ec *ErrorChan) AddNewRoutineError(routineAddr string, err error) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.ErrMap[routineAddr] = err
}

func (r *Room) DistributeMsg(fromUser string, msg string) *ErrorChan {
	errChan := ErrorChan{
		ErrMap: make(map[string]error),
	}

	var wg sync.WaitGroup
	for _, usr := range r.GetUsers() {
		if usr.Addr.String() != fromUser {
			wg.Add(1)
			go func(user User) {
				defer wg.Done()
				conn := *user.GetConnection()

				// output messages
				_, err := conn.Write([]byte(
					">" + usr.UserName + ": " + msg + "\n",
				))

				if err != nil {
					errChan.AddNewRoutineError(conn.RemoteAddr().String(), err)
				}
			}(usr)
		}
	}
	wg.Wait()
	return &errChan
}

func main() {
	server := NewServer(":2000")
	err := server.Listen()

	// create rooms
	rooms := []Room{
		{RoomID: 1, RoomName: "Common-open room"},
		{RoomID: 2, RoomName: "Common-lock room"},
		{RoomID: 3, RoomName: "Private room"},
	}
	for _, room := range rooms {
		server.Rooms[room.RoomID] = &room
	}

	if err != nil {
		log.Fatalf("Could not listening the port: %s\n", err.Error())
	}

	for {
		conn, err := server.Ln.Accept()
		if err != nil {
			log.Printf("Could not connect to the server: %s\n", err.Error())
			continue
		}

		go handleConnection(conn, server)
	}
}

func handleConnection(conn net.Conn, server *Server) {
	defer conn.Close()

	fmt.Fprintln(conn, "Your name:")
	scanner := bufio.NewScanner(conn)
	scanner.Scan()
	name := scanner.Text()

	var selectedRoom *Room = nil
	for {
		fmt.Fprintln(conn, "Choose the room (Common-open room [1]; Common-lock room [2]; Private room):")
		scanner := bufio.NewScanner(conn)
		scanner.Scan()
		choice := scanner.Text()

		switch choice {
		case "1":
			selectedRoom = server.Rooms[1]
		case "2":
			selectedRoom = server.Rooms[2]
			fmt.Fprintln(conn, "Enter the password for log in the common-lock room:")
			scanner3 := bufio.NewScanner(conn)
			scanner3.Scan()
			if scanner3.Text() != password3 {
				fmt.Fprintln(conn, "Wrong password. You can not be able to enter the room.")
				continue
			}
			fmt.Fprintf(conn, "The password is correct. You can enter \n")
		case "3":
			selectedRoom = server.Rooms[3]
		default:
			fmt.Fprintln(conn, "Wrong choose. You must choose between rooms 1, 2, or 3.")
			continue
		}

		if selectedRoom != nil {
			break
		}
	}

	addr := conn.RemoteAddr()

	user := User{
		Addr:     addr,
		conn:     &conn,
		RoomId:   selectedRoom.RoomID,
		Room:     selectedRoom,
		UserName: name,
	}

	selectedRoom.AddUser(user)

	for {

		// reading messages
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("Could not read message from user: %s\n", err.Error())
			return
		}

		// recieving messages
		msg := fmt.Sprintf("%s", (buf[:n]))
		errChan := selectedRoom.DistributeMsg(addr.String(), msg)
		if len(errChan.ErrMap) != 0 {
			log.Println("Some users did not recieve the message")
		}
	}
}
