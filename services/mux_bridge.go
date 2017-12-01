package services

import (
	"bufio"
	"io"
	"log"
	"net"
	"proxy/utils"
	"strconv"
	"time"

	"github.com/xtaci/smux"
)

type MuxBridge struct {
	cfg                MuxBridgeArgs
	clientControlConns utils.ConcurrentMap
}

func NewMuxBridge() Service {
	return &MuxBridge{
		cfg:                MuxBridgeArgs{},
		clientControlConns: utils.NewConcurrentMap(),
	}
}

func (s *MuxBridge) InitService() {

}
func (s *MuxBridge) CheckArgs() {
	if *s.cfg.CertFile == "" || *s.cfg.KeyFile == "" {
		log.Fatalf("cert and key file required")
	}
	s.cfg.CertBytes, s.cfg.KeyBytes = utils.TlsBytes(*s.cfg.CertFile, *s.cfg.KeyFile)
}
func (s *MuxBridge) StopService() {

}
func (s *MuxBridge) Start(args interface{}) (err error) {
	s.cfg = args.(MuxBridgeArgs)
	s.CheckArgs()
	s.InitService()
	host, port, _ := net.SplitHostPort(*s.cfg.Local)
	p, _ := strconv.Atoi(port)
	sc := utils.NewServerChannel(host, p)

	err = sc.ListenTls(s.cfg.CertBytes, s.cfg.KeyBytes, func(inConn net.Conn) {
		reader := bufio.NewReader(inConn)

		var err error
		var connType uint8
		var key string
		err = utils.ReadPacket(reader, &connType, &key)
		if err != nil {
			log.Printf("read error,ERR:%s", err)
			return
		}
		switch connType {
		case CONN_SERVER:
			log.Printf("server connection %s", key)
			session, err := smux.Server(inConn, nil)
			if err != nil {
				utils.CloseConn(&inConn)
				log.Printf("server session error,ERR:%s", err)
				return
			}
			for {
				stream, err := session.AcceptStream()
				if err != nil {
					session.Close()
					utils.CloseConn(&inConn)
					return
				}
				go s.callback(stream, key)
			}
		case CONN_CLIENT:

			log.Printf("client connection %s", key)
			session, err := smux.Client(inConn, nil)
			if err != nil {
				utils.CloseConn(&inConn)
				log.Printf("client session error,ERR:%s", err)
				return
			}
			s.clientControlConns.Set(key, session)
			//log.Printf("set client session,key: %s", key)
		}

	})
	if err != nil {
		return
	}
	log.Printf("proxy on mux bridge mode %s", (*sc.Listener).Addr())
	return
}
func (s *MuxBridge) Clean() {
	s.StopService()
}
func (s *MuxBridge) callback(inConn net.Conn, key string) {
	reader := bufio.NewReader(inConn)
	var err error
	var ID, clientLocalAddr, serverID string
	err = utils.ReadPacketData(reader, &ID, &clientLocalAddr, &serverID)
	if err != nil {
		log.Printf("read error,ERR:%s", err)
		return
	}
	packet := utils.BuildPacketData(ID, clientLocalAddr, serverID)
	try := 20
	for {
		try--
		if try == 0 {
			break
		}
		session, ok := s.clientControlConns.Get(key)
		if !ok {
			log.Printf("client %s session not exists", key)
			time.Sleep(time.Second * 3)
			continue
		}
		stream, err := session.(*smux.Session).OpenStream()
		if err != nil {
			log.Printf("%s client session open stream fail, err: %s, retrying...", key, err)
			time.Sleep(time.Second * 3)
			continue
		} else {
			_, err := stream.Write(packet)
			if err != nil {
				log.Printf("server %s stream write fail, err: %s, retrying...", key, err)
				time.Sleep(time.Second * 3)
				continue
			}
			log.Printf("server stream %s created", ID)
			go io.Copy(stream, inConn)
			io.Copy(inConn, stream)
			stream.Close()
			inConn.Close()
			log.Printf("server stream %s released", ID)
			break
		}
	}

}