package ini

import (
	"bot-net-in-go/models"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func LoadEnv() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("failed to load the env file ", err)
	}
}

func CreateDatabase() error {
	// Połączenie z PostgreSQL bez określenia bazy danych
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s sslmode=%s",
		os.Getenv("host"), os.Getenv("port"), os.Getenv("user"), os.Getenv("password"), os.Getenv("sslmode"))

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return err
	}
	defer db.Close()

	// Sprawdź czy baza istnieje
	var exists bool
	query := `SELECT EXISTS(SELECT datname FROM pg_catalog.pg_database WHERE datname = $1)`
	err = db.QueryRow(query, os.Getenv("name")).Scan(&exists)
	if err != nil {
		return err
	}

	// Utwórz bazę jeśli nie istnieje
	if !exists {
		_, err = db.Exec(fmt.Sprintf("CREATE DATABASE %s", os.Getenv("name")))
		if err != nil {
			return err
		}
		fmt.Printf("Baza danych '%s' została utworzona\n", os.Getenv("name"))
	} else {
		fmt.Printf("Baza danych '%s' już istnieje\n", os.Getenv("name"))
	}

	return nil
}

var DB *gorm.DB

func ConnectDB() {
	// Wczytaj plik .env
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Błąd wczytywania pliku .env:", err)
	}

	// Pobierz URL bazy danych
	dsn := os.Getenv("database_url")
	if dsn == "" {
		log.Fatal("Brak zmiennej database_url w .env")
	}

	// Połącz z bazą
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Nie udało się połączyć z bazą danych:", err)
	}

	// Auto-migracja
	err = db.AutoMigrate(&models.Video{}, &models.Tags{})
	if err != nil {
		log.Fatal("Migracja nie powiodła się:", err)
	}

	DB = db
	fmt.Println("Połączono z bazą danych!")
}
