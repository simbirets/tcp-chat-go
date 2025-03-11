package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
)

type Server struct {
	Addr  string
	Ln    net.Listener
	Rooms map[RoomID]*Room // Хранение всех комнат
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

	// Создание комнат
	rooms := []Room{
		{RoomID: 1, RoomName: "Комната 1"},
		{RoomID: 2, RoomName: "Комната 2"},
		{RoomID: 3, RoomName: "Комната 3"},
	}
	for _, room := range rooms {
		server.Rooms[room.RoomID] = &room
	}

	if err != nil {
		log.Fatalf("Не удалось прослушивать порт: %s\n", err.Error())
	}

	for {
		conn, err := server.Ln.Accept()
		if err != nil {
			log.Printf("Не удалось подключиться к серверу: %s\n", err.Error())
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
	name := scanner.Text() // Get user's name

	var selectedRoom *Room = nil // Initialize selectedRoom to nil
	for {
		// Choice of room
		fmt.Fprintln(conn, "Выберите комнату (1, 2 или 3):")
		scanner2 := bufio.NewScanner(conn)
		scanner2.Scan()
		choice := scanner2.Text()

		switch choice {
		case "1":
			selectedRoom = server.Rooms[1]
		case "2":
			selectedRoom = server.Rooms[2]
		case "3":
			selectedRoom = server.Rooms[3]
		default:
			fmt.Fprintln(conn, "Неверный выбор. Пожалуйста, выберите 1, 2 или 3.")
			continue // Prompt for room selection again
		}

		// Exit the loop when a valid room is selected
		if selectedRoom != nil {
			break
		}
	}

	addr := conn.RemoteAddr() // Retaining the addr for adding user

	// Add user only when we have a valid selectedRoom
	user := User{
		Addr:     addr,
		conn:     &conn,
		RoomId:   selectedRoom.RoomID,
		Room:     selectedRoom,
		UserName: name,
	}

	selectedRoom.AddUser(user)

	for {
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("Ошибка чтения сообщения от пользователя: %s\n", err.Error())
			return
		}
		msg := fmt.Sprintf("%s", string(buf[:n])) // Changed to only send the raw message
		errChan := selectedRoom.DistributeMsg(addr.String(), msg)
		if len(errChan.ErrMap) != 0 {
			log.Println("Некоторые пользователи не получили сообщение")
		}
	}
}
