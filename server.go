package main

import (
	"github.com/nu7hatch/gouuid"
	"github.com/clawio/service-auth/lib"
	pb "github.com/clawio/service-localfsxattr-mysqlprop/proto/propagator"
	"github.com/jinzhu/gorm"
	rus "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"path"
	"strings"
	"time"
)

const (
	dirPerm           = 0755
	maxSQLIdle        = 100
	maxSQLConcurrency = 1000
)

var (
	unauthenticatedError = grpc.Errorf(codes.Unauthenticated, "identity not found")
	permissionDenied     = grpc.Errorf(codes.PermissionDenied, "access denied")
)

// debugLogger satisfies Gorm's logger interface
// so that we can log SQL queries at Logrus' debug level
type debugLogger struct{}

func (*debugLogger) Print(msg ...interface{}) {
	rus.Debug(msg)
}

type newServerParams struct {
	dsn               string
	db                *gorm.DB
	sharedSecret      string
	maxSqlIdle        int
	maxSqlConcurrency int
}

func newServer(p *newServerParams) (*server, error) {

	db, err := newDB("mysql", p.dsn)
	if err != nil {
		rus.Error(err)
		return nil, err
	}

	db.LogMode(true)
	db.SetLogger(&debugLogger{})
	db.DB().SetMaxIdleConns(p.maxSqlIdle)
	db.DB().SetMaxOpenConns(p.maxSqlConcurrency)

	err = db.AutoMigrate(&record{}).Error
	if err != nil {
		rus.Error(err)
		return nil, err
	}

	rus.Infof("automigration applied")

	s := &server{}
	s.p = p
	s.db = db
	return s, nil
}

type server struct {
	p  *newServerParams
	db *gorm.DB
}

func (s *server) Get(ctx context.Context, req *pb.GetReq) (*pb.Record, error) {

	traceID, err := getGRPCTraceID(ctx)
	if err != nil {
		rus.Error(err)
		return &pb.Record{}, err
	}
	log := rus.WithField("trace", traceID).WithField("svc", serviceID)
	ctx = newGRPCTraceContext(ctx, traceID)

	log.Info("request started")

	// Time request
	reqStart := time.Now()

	defer func() {
		// Compute request duration
		reqDur := time.Since(reqStart)

		// Log access info
		log.WithFields(rus.Fields{
			"method":   "get",
			"type":     "grpcaccess",
			"duration": reqDur.Seconds(),
		}).Info("request finished")

	}()

	idt, err := lib.ParseToken(req.AccessToken, s.p.sharedSecret)
	if err != nil {
		log.Error(err)
		return &pb.Record{}, unauthenticatedError
	}

	log.Infof("%s", idt)

	p := path.Clean(req.Path)

	log.Infof("path is %s", p)

	var rec *record

	rec, err = s.getByPath(p)
	if err != nil {
		log.Error(err)
		if err != gorm.RecordNotFound {
			return &pb.Record{}, err
		}

		if !req.ForceCreation {
			return &pb.Record{}, err
		}

		if req.ForceCreation {
			in := &pb.PutReq{}
			in.AccessToken = req.AccessToken
			in.Path = req.Path
			_, e := s.Put(ctx, in)
			if e != nil {
				return &pb.Record{}, err
			}

			rec, err = s.getByPath(p)
			if err != nil {
				return &pb.Record{}, nil
			}
		}
	}

	r := &pb.Record{}
	r.Path = rec.Path
	r.Etag = rec.ETag
	r.Modified = rec.MTime
	return r, nil
}

func (s *server) Mv(ctx context.Context, req *pb.MvReq) (*pb.Void, error) {

	traceID, err := getGRPCTraceID(ctx)
	if err != nil {
		rus.Error(err)
		return &pb.Void{}, err
	}
	log := rus.WithField("trace", traceID).WithField("svc", serviceID)
	ctx = newGRPCTraceContext(ctx, traceID)

	log.Info("request started")

	// Time request
	reqStart := time.Now()

	defer func() {
		// Compute request duration
		reqDur := time.Since(reqStart)

		// Log access info
		log.WithFields(rus.Fields{
			"method":   "mv",
			"type":     "grpcaccess",
			"duration": reqDur.Seconds(),
		}).Info("request finished")

	}()

	idt, err := lib.ParseToken(req.AccessToken, s.p.sharedSecret)
	if err != nil {
		log.Error(err)
		return &pb.Void{}, unauthenticatedError
	}

	log.Infof("%s", idt)

	src := path.Clean(req.Src)
	dst := path.Clean(req.Dst)

	log.Infof("src path is %s", src)
	log.Infof("dst path is %s", dst)

	recs, err := s.getRecordsWithPathPrefix(src)
	if err != nil {
		log.Error(err)
		return &pb.Void{}, nil

	}

	tx := s.db.Begin()
	for _, rec := range recs {
		newPath := path.Join(dst, path.Clean(strings.TrimPrefix(rec.Path, src)))
		log.Infof("src path %s will be renamed to %s", rec.Path, newPath)

		err = s.db.Model(record{}).Where("path=?", rec.Path).Updates(record{Path: newPath}).Error
		if err != nil {
			log.Error(err)
			tx.Rollback()
			return &pb.Void{}, err
		}
	}
	tx.Commit()

	log.Infof("renamed %d entries", len(recs))
	rawUUID, err := uuid.NewV4()
	if err != nil {
		return &pb.Void{}, err
	}
	etag := rawUUID.String()
	mtime := uint32(time.Now().Unix())
	err = s.propagateChanges(ctx, dst, etag, mtime, "")
	if err != nil {
		log.Error(err)
	}

	log.Infof("propagated changes till %s", "")

	return &pb.Void{}, nil
}

func (s *server) getRecordsWithPathPrefix(p string) ([]record, error) {

	var recs []record

	err := s.db.Where("path LIKE ? OR path=?", p+"/%", p).Find(&recs).Error
	if err != nil {
		return recs, nil
	}

	return recs, nil
}
func (s *server) Rm(ctx context.Context, req *pb.RmReq) (*pb.Void, error) {

	traceID, err := getGRPCTraceID(ctx)
	if err != nil {
		rus.Error(err)
		return &pb.Void{}, err
	}
	log := rus.WithField("trace", traceID).WithField("svc", serviceID)
	ctx = newGRPCTraceContext(ctx, traceID)

	log.Info("request started")

	// Time request
	reqStart := time.Now()

	defer func() {
		// Compute request duration
		reqDur := time.Since(reqStart)

		// Log access info
		log.WithFields(rus.Fields{
			"method":   "rm",
			"type":     "grpcaccess",
			"duration": reqDur.Seconds(),
		}).Info("request finished")

	}()

	idt, err := lib.ParseToken(req.AccessToken, s.p.sharedSecret)
	if err != nil {
		log.Error(err)
		return &pb.Void{}, unauthenticatedError
	}

	log.Infof("%s", idt)

	p := path.Clean(req.Path)

	log.Infof("path is %s", p)

	ts := time.Now().Unix()
	err = s.db.Where("(path LIKE ? OR path=? ) AND m_time < ?", p+"/%", p, ts).Delete(record{}).Error
	if err != nil {
		log.Error(err)
		return &pb.Void{}, err
	}

	rawUUID, err := uuid.NewV4()
	if err != nil {
		return &pb.Void{}, err
	}
	err = s.propagateChanges(ctx, p, rawUUID.String(), uint32(ts), "")
	if err != nil {
		log.Error(err)
	}

	log.Infof("propagated changes till %s", "")

	return &pb.Void{}, nil
}

func (s *server) Put(ctx context.Context, req *pb.PutReq) (*pb.Void, error) {

	traceID, err := getGRPCTraceID(ctx)
	if err != nil {
		rus.Error(err)
		return &pb.Void{}, err
	}
	log := rus.WithField("trace", traceID).WithField("svc", serviceID)
	ctx = newGRPCTraceContext(ctx, traceID)

	log.Info("request started")

	// Time request
	reqStart := time.Now()

	defer func() {
		// Compute request duration
		reqDur := time.Since(reqStart)

		// Log access info
		log.WithFields(rus.Fields{
			"method":   "put",
			"type":     "grpcaccess",
			"duration": reqDur.Seconds(),
		}).Info("request finished")

	}()

	idt, err := lib.ParseToken(req.AccessToken, s.p.sharedSecret)
	if err != nil {
		log.Error(err)
		return &pb.Void{}, unauthenticatedError
	}

	log.Infof("%s", idt)

	p := path.Clean(req.Path)

	log.Infof("path is %s", p)

	rawUUID, err := uuid.NewV4()
	if err != nil {
		return &pb.Void{}, err
	}
	var etag = rawUUID.String()
	var mtime = uint32(time.Now().Unix())

	log.Infof("new record will have path=%s etag=%s mtime=%d", p, etag, mtime)

	err = s.insert(p, etag, mtime)
	if err != nil {
		log.Error(err)
		return &pb.Void{}, err
	}

	log.Infof("new record saved to db")

	err = s.propagateChanges(ctx, p, etag, mtime, "")
	if err != nil {
		log.Error(err)
	}

	log.Infof("propagated changes till ancestor %s", "")

	return &pb.Void{}, nil
}

func (s *server) getByPath(path string) (*record, error) {

	r := &record{}
	err := s.db.Where("path=?", path).First(r).Error
	return r, err
}

func (s *server) insert(p, etag string, mtime uint32) error {

	err := s.db.Exec(`INSERT INTO records (path, e_tag, m_time) VALUES (?,?,?)
	ON DUPLICATE KEY UPDATE e_tag=VALUES(e_tag), m_time=VALUES(m_time)`,
		p, etag, mtime).Error

	if err != nil {
		return err
	}

	return nil
}
func (s *server) update(p, etag string, mtime uint32) int64 {

	return s.db.Model(record{}).Where("path=? AND m_time < ?", p, mtime).Updates(record{ETag: etag, MTime: mtime}).RowsAffected
}

// propagateChanges propagates mtime and etag until the user home directory
// This propagation is needed for the client discovering changes
// Ex: given the succesfull upload of the file /local/users/d/demo/photos/1.png
// the etag and mtime will be propagated to:
//    - /local/users/d/demo/photos
//    - /local/users/d/demo
func (s *server) propagateChanges(ctx context.Context, p, etag string, mtime uint32, stopPath string) error {

	traceID, err := getGRPCTraceID(ctx)
	if err != nil {
		rus.Error(err)
		return err
	}
	log := rus.WithField("trace", traceID).WithField("svc", serviceID)
	ctx = newGRPCTraceContext(ctx, traceID)

	// TODO(labkode) assert the list ordered from most deeper to less so we can shortcircuit
	// after first miss
	paths := getPathsTillHome(ctx, p)
	for _, p := range paths {
		numRows := s.update(p, etag, mtime)
		if numRows == 0 {
			log.Warnf("parent path %s has been updated in the meanwhile so we do not override with old info. Propagation stopped", p)
			// Following the CAS tree approach it does not make sense to update\
			// parents if child has been updated wit new info
			break
		}
		log.Infof("parent path %s has being updated", p)
	}

	return nil
}

func getPathsTillHome(ctx context.Context, p string) []string {

	paths := []string{}
	tokens := strings.Split(p, "/")

	if len(tokens) < 5 {
		// if not under home dir we do not propagate
		return paths
	}

	homeTokens := tokens[0:5]
	restTokens := tokens[5:]

	home := path.Clean("/" + path.Join(homeTokens...))

	previous := home
	paths = append(paths, previous)

	for _, token := range restTokens {
		previous = path.Join(previous, path.Clean(token))
		paths = append(paths, previous)
	}

	if len(paths) >= 1 {
		paths = paths[:len(paths)-1] // remove inserted/updated path from paths to update
	}

	//reverse it to have deeper paths first to shortcircuit
	for i := len(paths)/2 - 1; i >= 0; i-- {
		opp := len(paths) - 1 - i
		paths[i], paths[opp] = paths[opp], paths[i]

	}
 
	return paths
}
