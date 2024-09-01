package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// JSONData represents the structure of each JSON entry
type JSONData struct {
	Host    string   `json:"host"`
	Input   string   `json:"input"`
	Sources []string `json:"sources"`
}

// DBManager manages the SQLite database
type DBManager struct {
	db *sql.DB
}

// NewDBManager initializes the database manager with a given database file path
func NewDBManager(dbPath string) (*DBManager, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	return &DBManager{db: db}, nil
}

// TableExists checks if a table already exists in the database
func (manager *DBManager) TableExists(tableName string) (bool, error) {
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name=?;`
	row := manager.db.QueryRow(query, tableName)

	var name string
	err := row.Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CreateTable creates a new table for the specified domain (if not exists)
func (manager *DBManager) CreateTable(tableName string) error {
	exists, err := manager.TableExists(tableName)
	if err != nil {
		return err
	}

	if !exists {
		query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS "%s" (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host TEXT,
			input TEXT,
			sources TEXT,
			timestamp DATETIME,
			UNIQUE(host, input)
		);`, tableName)

		_, err = manager.db.Exec(query)
		if err != nil {
			return err
		}
	}

	return nil
}

// RecordExists checks if a record with the same host and input already exists in the table
func (manager *DBManager) RecordExists(tableName, host, input string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM \"%s\" WHERE host = ? AND input = ? LIMIT 1;", tableName)
	row := manager.db.QueryRow(query, host, input)

	var exists int
	err := row.Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertData inserts a JSON entry into the corresponding table if it doesn't exist
func (manager *DBManager) InsertData(tableName string, data JSONData) error {
	exists, err := manager.RecordExists(tableName, data.Host, data.Input)
	if err != nil {
		return err
	}

	if !exists {
		sources := strings.Join(data.Sources, ",")
		timestamp := time.Now().Format("2006-01-02 15:04:05")

		query := fmt.Sprintf("INSERT INTO \"%s\" (host, input, sources, timestamp) VALUES (?, ?, ?, ?);", tableName)

		_, err = manager.db.Exec(query, data.Host, data.Input, sources, timestamp)
		if err != nil {
			return err
		}
	}

	return nil
}

// JSONProcessor handles the processing of JSON files and data
type JSONProcessor struct {
	dbManager *DBManager
}

// NewJSONProcessor creates a new JSONProcessor
func NewJSONProcessor(dbManager *DBManager) *JSONProcessor {
	return &JSONProcessor{dbManager: dbManager}
}

// ProcessFile processes a single JSON file, handling both single and multiple JSON objects
func (jp *JSONProcessor) ProcessFile(filePath string) error {
	file, err := ioutil.ReadFile(filePath)
	if err != nil {
		return err
	}

	var jsonDataArray []JSONData
	if err := json.Unmarshal(file, &jsonDataArray); err != nil {
		// Handle as newline-delimited JSON
		return jp.processNDJSON(filePath, string(file))
	}

	// Handle as a JSON array
	for _, jsonData := range jsonDataArray {
		if err := jp.processJSONData(jsonData); err != nil {
			return err
		}
	}

	return nil
}

func (jp *JSONProcessor) processNDJSON(filePath, fileContent string) error {
	scanner := bufio.NewScanner(strings.NewReader(fileContent))
	for scanner.Scan() {
		line := scanner.Text()

		var jsonData JSONData
		if err := json.Unmarshal([]byte(line), &jsonData); err != nil {
			return fmt.Errorf("error parsing JSON in file %s: %v", filePath, err)
		}

		if err := jp.processJSONData(jsonData); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func (jp *JSONProcessor) processJSONData(jsonData JSONData) error {
	tableName := jsonData.Input

	if err := jp.dbManager.CreateTable(tableName); err != nil {
		return err
	}

	if err := jp.dbManager.InsertData(tableName, jsonData); err != nil {
		return err
	}

	return nil
}

// ProcessFilesInDir processes all JSON files in the input directory
func (jp *JSONProcessor) ProcessFilesInDir(inputDir string) error {
	files, err := ioutil.ReadDir(inputDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			err := jp.ProcessFile(filepath.Join(inputDir, file.Name()))
			if err != nil {
				return err
			}
			fmt.Printf("Processed file: %s\n", file.Name())
		}
	}

	return nil
}

// ConvertSubdomainListToJSON converts a list of subdomains to a JSON file that the program can process
func ConvertSubdomainListToJSON(subdomainListPath, outputPath, inputDomain string) error {
	file, err := os.Open(subdomainListPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var jsonEntries []JSONData

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		subdomain := scanner.Text()

		jsonEntry := JSONData{
			Host:    subdomain,
			Input:   inputDomain,
			Sources: []string{"Admin"},
		}
		jsonEntries = append(jsonEntries, jsonEntry)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	jsonData, err := json.Marshal(jsonEntries)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(outputPath, jsonData, 0644)
}

// PrintUsage prints the help menu
func PrintUsage() {
	fmt.Println("Usage:")
	fmt.Println("  subp -i <input_dir> -o <output_dir> -db <db_name> [-tb <table_name>] [-jsfile <file_name>] [-l <file_name> -ind <domain>]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  -i, --input         Directory containing JSON files")
	fmt.Println("  -o, --output        Directory to save SQLite DB file")
	fmt.Println("  -db, --dbname       SQLite DB file name (default: data.db)")
	fmt.Println("  -tb, --tablename    Optional: Specify the table name to append data")
	fmt.Println("  -jsfile, --jsonfilename  Optional: Specify a specific JSON file to process")
	fmt.Println("  -l, --subdomainlist Optional: Provide a file with a list of subdomains to convert to JSON and process")
	fmt.Println("  -ind, --inputdomain Input domain to be used in the generated JSON (required with --subdomainlist)")
	fmt.Println("  -h, --help          Show help menu")
}

func main() {
	// Define command-line flags with both long and short options
	inputDir := flag.String("input", "", "Directory containing JSON files")
	flag.StringVar(inputDir, "i", "", "Directory containing JSON files")

	outputDir := flag.String("output", "", "Directory to save SQLite DB file")
	flag.StringVar(outputDir, "o", "", "Directory to save SQLite DB file")

	dbName := flag.String("dbname", "data.db", "SQLite DB file name")
	flag.StringVar(dbName, "db", "data.db", "SQLite DB file name")

	jsonFileName := flag.String("jsonfilename", "", "Optional: Specify a specific JSON file to process")
	flag.StringVar(jsonFileName, "jsfile", "", "Optional: Specify a specific JSON file to process")

	subdomainList := flag.String("subdomainlist", "", "Optional: Provide a file with a list of subdomains to convert to JSON and process")
	flag.StringVar(subdomainList, "l", "", "Optional: Provide a file with a list of subdomains to convert to JSON and process")

	inputDomain := flag.String("inputdomain", "", "Input domain to be used in the generated JSON (required with --subdomainlist)")
	flag.StringVar(inputDomain, "ind", "", "Input domain to be used in the generated JSON (required with --subdomainlist)")

	help := flag.Bool("help", false, "Show help menu")
	flag.BoolVar(help, "h", false, "Show help menu")

	flag.Parse()

	// Show help menu if -h or --help flag is set
	if *help {
		PrintUsage()
		return
	}

	if *inputDir == "" || *outputDir == "" {
		fmt.Println("Error: Input and output directories must be specified.")
		PrintUsage()
		os.Exit(1)
	}

	if _, err := os.Stat(*outputDir); errors.Is(err, os.ErrNotExist) {
		err := os.MkdirAll(*outputDir, 0755)
		if err != nil {
			fmt.Printf("Error creating output directory: %v\n", err)
			os.Exit(1)
		}
	}

	dbPath := filepath.Join(*outputDir, *dbName)
	dbManager, err := NewDBManager(dbPath)
	if err != nil {
		fmt.Printf("Error initializing database: %v\n", err)
		os.Exit(1)
	}
	defer dbManager.db.Close()

	jsonProcessor := NewJSONProcessor(dbManager)

	if *subdomainList != "" {
		if *inputDomain == "" {
			fmt.Println("Error: --inputdomain is required when using --subdomainlist")
			PrintUsage()
			os.Exit(1)
		}
		tempJSONFile := filepath.Join(*inputDir, "subdomains_temp.json")
		err := ConvertSubdomainListToJSON(*subdomainList, tempJSONFile, *inputDomain)
		if err != nil {
			fmt.Printf("Error converting subdomain list to JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Converted subdomain list to JSON: %s\n", tempJSONFile)
		err = jsonProcessor.ProcessFile(tempJSONFile)
		if err != nil {
			fmt.Printf("Error processing generated JSON file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Processed file: %s\n", tempJSONFile)
		os.Remove(tempJSONFile)
	} else if *jsonFileName != "" {
		filePath := filepath.Join(*inputDir, *jsonFileName)
		err = jsonProcessor.ProcessFile(filePath)
		if err != nil {
			fmt.Printf("Error processing JSON file %s: %v\n", *jsonFileName, err)
			os.Exit(1)
		}
		fmt.Printf("Processed file: %s\n", *jsonFileName)
	} else {
		err = jsonProcessor.ProcessFilesInDir(*inputDir)
		if err != nil {
			fmt.Printf("Error processing JSON files: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("Database saved at: %s\n", dbPath)
}



