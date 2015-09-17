// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/rand"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/juju/errors"
	"github.com/ngaut/log"
	mysql "github.com/pingcap/tidb/mysqldef"
	"github.com/pingcap/tidb/util/arena"
)

var (
	baseConnID uint32 = 10000
)

// Server is the MySQL protocol server
type Server struct {
	cfg               *Config
	driver            IDriver
	listener          net.Listener
	rwlock            *sync.RWMutex
	concurrentLimiter *TokenLimiter
	clients           map[uint32]*clientConn
}

func (s *Server) getToken() *Token {
	return s.concurrentLimiter.Get()
}

func (s *Server) releaseToken(token *Token) {
	s.concurrentLimiter.Put(token)
}

func (s *Server) newConn(conn net.Conn) (cc *clientConn, err error) {
	log.Info("newConn", conn.RemoteAddr().String())
	cc = &clientConn{
		conn:         conn,
		pkg:          newPacketIO(conn),
		server:       s,
		connectionID: atomic.AddUint32(&baseConnID, 1),
		collation:    mysql.DefaultCollationID,
		charset:      mysql.DefaultCharset,
		alloc:        arena.NewAllocator(32 * 1024),
	}
	cc.salt = make([]byte, 20)
	io.ReadFull(rand.Reader, cc.salt)
	for i, b := range cc.salt {
		if b == 0 {
			cc.salt[i] = '0'
		}
	}
	return
}

func (s *Server) skipAuth() bool {
	return s.cfg.SkipAuth
}

func (s *Server) cfgGetPwd(user string) string {
	return s.cfg.Password // TODO: support multiple users
}

// NewServer creates a new Server.
func NewServer(cfg *Config, driver IDriver) (*Server, error) {
	s := &Server{
		cfg:               cfg,
		driver:            driver,
		concurrentLimiter: NewTokenLimiter(100),
		rwlock:            &sync.RWMutex{},
		clients:           make(map[uint32]*clientConn),
	}

	var err error
	s.listener, err = net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return nil, errors.Trace(err)
	}

	log.Infof("Server run MySql Protocol Listen at [%s]", s.cfg.Addr)
	return s, nil
}

// Run runs the server.
func (s *Server) Run() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			log.Errorf("accept error %s", err.Error())
			return errors.Trace(err)
		}

		go s.onConn(conn)
	}
}

// Close closes the server.
func (s *Server) Close() {
	s.rwlock.Lock()
	defer s.rwlock.Unlock()

	if s.listener != nil {
		s.listener.Close()
		s.listener = nil
	}
}

func (s *Server) onConn(c net.Conn) {
	conn, err := s.newConn(c)
	if err != nil {
		log.Errorf("newConn error %s", errors.ErrorStack(err))
		return
	}
	if err := conn.handshake(); err != nil {
		log.Errorf("handshake error %s", errors.ErrorStack(err))
		c.Close()
		return
	}
	conn.ctx, err = s.driver.OpenCtx(conn.capability, uint8(conn.collation), conn.dbname)
	if err != nil {
		log.Errorf("open ctx error %s", errors.ErrorStack(err))
		c.Close()
		return
	}

	defer func() {
		log.Infof("close %s", conn)
	}()

	s.rwlock.Lock()
	s.clients[conn.connectionID] = conn
	s.rwlock.Unlock()

	conn.Run()
}
