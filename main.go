package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	_ "github.com/mattn/go-sqlite3"
)

const (
	lockedRoomPassword = "1234"
)

var db *sql.DB

type RoomID int

type User struct {
	Addr     net.Addr
	RoomID   RoomID
	Conn     net.Conn
	Room     *Room
	UserName string
}

type Room struct {
	RoomID   RoomID
	RoomName string
	users    []User
	mu       sync.Mutex
}

func (r *Room) AddUser(user User) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Addr.String() == user.Addr.String() {
			return false
		}
	}
	r.users = append(r.users, user)
	return true
}

func (r *Room) RemoveUser(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, u := range r.users {
		if u.Addr.String() == addr {
			r.users = append(r.users[:i], r.users[i+1:]...)
			return
		}
	}
}

func (r *Room) DistributeMsg(senderName, msg string) {
	r.mu.Lock()
	users := make([]User, len(r.users))
	copy(users, r.users)
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, user := range users {
		if user.UserName == senderName {
			continue
		}
		wg.Add(1)
		go func(u User) {
			defer wg.Done()
			line := fmt.Sprintf("> %s: %s\n", senderName, strings.TrimSpace(msg))
			if _, err := u.Conn.Write([]byte(line)); err != nil {
				log.Printf("Send error to %s: %v", u.Addr, err)
				u.Room.RemoveUser(u.Addr.String())
				u.Conn.Close()
			}
		}(user)
	}
	wg.Wait()

	if r.RoomID == 1 {
		trimmedMsg := strings.TrimSpace(msg)
		if trimmedMsg != "" {
			_, err := db.Exec(
				"INSERT INTO messages (room_id, username, message) VALUES (?, ?, ?)",
				r.RoomID, senderName, trimmedMsg,
			)
			if err != nil {
				log.Printf("Failed to save message to DB: %v", err)
			}
		}
	}
}

func (r *Room) DistributeSystemMsg(msg string) {
	r.mu.Lock()
	users := make([]User, len(r.users))
	copy(users, r.users)
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, user := range users {
		wg.Add(1)
		go func(u User) {
			defer wg.Done()
			line := fmt.Sprintf("*** %s ***\n", strings.TrimSpace(msg))
			if _, err := u.Conn.Write([]byte(line)); err != nil {
				log.Printf("System msg send error to %s: %v", u.Addr, err)
				u.Room.RemoveUser(u.Addr.String())
				u.Conn.Close()
			}
		}(user)
	}
	wg.Wait()
}

func (r *Room) DistributeSystemMsgToOthers(excludeAddr, msg string) {
	r.mu.Lock()
	users := make([]User, len(r.users))
	copy(users, r.users)
	r.mu.Unlock()

	var wg sync.WaitGroup
	for _, user := range users {
		if user.Addr.String() == excludeAddr {
			continue
		}
		wg.Add(1)
		go func(u User) {
			defer wg.Done()
			line := fmt.Sprintf("*** %s ***\n", strings.TrimSpace(msg))
			if _, err := u.Conn.Write([]byte(line)); err != nil {
				log.Printf("System msg send error to %s: %v", u.Addr, err)
				u.Room.RemoveUser(u.Addr.String())
				u.Conn.Close()
			}
		}(user)
	}
	wg.Wait()
}

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
	if err != nil {
		return err
	}
	s.Ln = ln
	return nil
}

func initDB() error {
	var err error
	db, err = sql.Open("sqlite3", "./chat.db")
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			room_id INTEGER NOT NULL,
			username TEXT NOT NULL,
			message TEXT NOT NULL,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

func handleConnection(conn net.Conn, server *Server) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)

	fmt.Fprintln(conn, "Your name:")
	if !scanner.Scan() {
		return
	}
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		fmt.Fprintln(conn, "Name cannot be empty. Disconnecting.")
		return
	}

	for {
		var selectedRoom *Room
		for selectedRoom == nil {
			fmt.Fprintln(conn, "\nChoose room:\n[1] Common-open room\n[2] Common-lock room\n[3] Private room\n[q] Quit chat")
			if !scanner.Scan() {
				return
			}
			choice := strings.TrimSpace(scanner.Text())

			if choice == "q" || choice == "quit" {
				fmt.Fprintln(conn, "Goodbye!")
				return
			}

			switch choice {
			case "1":
				selectedRoom = server.Rooms[1]
			case "2":
				selectedRoom = server.Rooms[2]
				fmt.Fprintln(conn, "Password for locked room:")
				if !scanner.Scan() {
					return
				}
				if scanner.Text() != lockedRoomPassword {
					fmt.Fprintln(conn, "Wrong password. Try again.")
					selectedRoom = nil
					continue
				}
				fmt.Fprintln(conn, "Access granted.")
			case "3":
				selectedRoom = server.Rooms[3]
			default:
				fmt.Fprintln(conn, "Invalid choice. Use 1, 2, 3, or 'q'.")
				continue
			}
		}

		user := User{
			Addr:     conn.RemoteAddr(),
			Conn:     conn,
			RoomID:   selectedRoom.RoomID,
			Room:     selectedRoom,
			UserName: name,
		}

		if !selectedRoom.AddUser(user) {
			fmt.Fprintln(conn, "You are already in this room!")
			continue
		}

		selectedRoom.DistributeSystemMsg(fmt.Sprintf("%s has joined the room", user.UserName))

		func() {
			defer func() {
				selectedRoom.DistributeSystemMsgToOthers(user.Addr.String(), fmt.Sprintf("%s has left the room", user.UserName))
				selectedRoom.RemoveUser(user.Addr.String())
				log.Printf("User %s left room %s", user.UserName, selectedRoom.RoomName)
			}()

			fmt.Fprintf(conn, "\nWelcome to %s, %s!\n", selectedRoom.RoomName, user.UserName)
			fmt.Fprintln(conn, "Type /exit to leave room, /quit to disconnect.")

			if selectedRoom.RoomID == 1 {
				rows, err := db.Query(`
					SELECT username, message, timestamp 
					FROM messages 
					WHERE room_id = ? 
					ORDER BY timestamp DESC 
					LIMIT 10`, 1)
				if err == nil {
					var history []string
					for rows.Next() {
						var user, msg, ts string
						rows.Scan(&user, &msg, &ts)
						history = append(history, fmt.Sprintf("  [%s] %s: %s", ts, user, msg))
					}
					rows.Close()
					if len(history) > 0 {
						fmt.Fprintln(conn, "\nLast 10 messages:")
						for i := len(history) - 1; i >= 0; i-- {
							fmt.Fprintln(conn, history[i])
						}
						fmt.Fprintln(conn, "")
					}
				}
			}

			for scanner.Scan() {
				line := scanner.Text()
				cmd := strings.TrimSpace(line)

				if cmd == "/quit" {
					fmt.Fprintln(conn, "Disconnecting from chat...")
					return
				}
				if cmd == "/exit" {
					fmt.Fprintln(conn, "\nLeaving room...")
					return
				}

				if cmd == "" {
					continue
				}

				selectedRoom.DistributeMsg(user.UserName, line)
			}

			if err := scanner.Err(); err != nil {
				log.Printf("Read error from %s: %v", user.Addr, err)
			}
		}()
	}
}

func main() {
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	server := NewServer(":2000")
	if err := server.Listen(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	defer server.Ln.Close()

	server.Rooms[1] = &Room{RoomID: 1, RoomName: "Common-open room"}
	server.Rooms[2] = &Room{RoomID: 2, RoomName: "Common-lock room"}
	server.Rooms[3] = &Room{RoomID: 3, RoomName: "Private room"}

	log.Println("Chat server with SQLite persistence running on :2000")

	for {
		conn, err := server.Ln.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		log.Printf("New connection from %s", conn.RemoteAddr())
		go handleConnection(conn, server)
	}
}