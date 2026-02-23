// migrate_user_data 将老用户的数据"过户"到新用户
// 用法: go run scripts/migrate_user_data.go [ -db data/data.db ]
package main

import (
	"database/sql"
	"flag"
	"log"
	"os"

	_ "github.com/glebarez/sqlite"
)

const (
	oldUserID = "5444711b-4815-4dfc-8f59-338454a367a0"
	newUserID = "fdb35a71-8557-4183-8fc4-d30000517141"
)

func main() {
	dbPath := flag.String("db", "", "数据库路径，默认 data/data.db 或 data/db/data.db")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = os.Getenv("DB_PATH")
	}
	if *dbPath == "" {
		if _, err := os.Stat("data/data.db"); err == nil {
			*dbPath = "data/data.db"
		} else if _, err := os.Stat("data/db/data.db"); err == nil {
			*dbPath = "data/db/data.db"
		} else {
			*dbPath = "data/data.db"
		}
	}

	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		log.Fatalf("❌ 数据库文件不存在: %s", *dbPath)
	}

	log.Printf("📂 连接数据库: %s", *dbPath)
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("❌ 连接失败: %v", err)
	}
	defer db.Close()

	queries := []struct {
		name string
		sql  string
	}{
		{"exchanges", "UPDATE exchanges SET user_id = ? WHERE user_id = ?"},
		{"ai_models", "UPDATE ai_models SET user_id = ? WHERE user_id = ?"},
		{"traders", "UPDATE traders SET user_id = ? WHERE user_id = ?"},
	}

	for _, q := range queries {
		res, err := db.Exec(q.sql, newUserID, oldUserID)
		if err != nil {
			log.Fatalf("❌ %s 更新失败: %v", q.name, err)
		}
		n, _ := res.RowsAffected()
		log.Printf("✓ %s: 已过户 %d 条", q.name, n)
	}

	log.Println("✅ 数据迁移完成，请重启程序")
}
