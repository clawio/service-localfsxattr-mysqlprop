package main

import (
	"fmt"
	"github.com/nu7hatch/gouuid"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jinzhu/gorm"
	"golang.org/x/net/context"
	metadata "google.golang.org/grpc/metadata"
)

// TODO(labkode) set collation for table and column to utf8. The default is swedish
type record struct {
	Path  string `sql:"unique_index:idx_path"`
	ETag  string
	MTime uint32
}

func (r *record) String() string {
	return fmt.Sprintf("path=%s etag=%s mtime=%d",
		r.Path, r.ETag, r.MTime)
}
func newDB(driver, dsn string) (*gorm.DB, error) {

	db, err := gorm.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	db.AutoMigrate(&record{})

	return &db, nil
}
func newGRPCTraceContext(ctx context.Context, trace string) context.Context {
	md := metadata.Pairs("trace", trace)
	ctx = metadata.NewContext(ctx, md)
	return ctx
}

func getGRPCTraceID(ctx context.Context) (string, error) {

	md, ok := metadata.FromContext(ctx)
	if !ok {
		rawUUID, err := uuid.NewV4()
		if err != nil {
			return "", err
		}
		return rawUUID.String(), nil
	}

	tokens := md["trace"]
	if len(tokens) == 0 {
		rawUUID, err := uuid.NewV4()
		if err != nil {
			return "", err
		}
		return rawUUID.String(), nil
	}

	if tokens[0] != "" {
		return tokens[0], nil
	}
	rawUUID, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	return rawUUID.String(), nil
}
