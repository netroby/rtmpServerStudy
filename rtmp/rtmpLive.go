package rtmp

import (
	"container/list"
	"fmt"
	"time"
	"rtmpServerStudy/AvQue"
	"context"
	"rtmpServerStudy/amf"
	"rtmpServerStudy/av"
	"rtmpServerStudy/flv"
	"rtmpServerStudy/flv/flvio"
)

func (self *Session)rtmpClosePublishingSession(){
	RtmpSessionDel(self)
	self.cancel()
	self.isClosed = true
	var next *list.Element
	CursorList := self.CursorList.GetList()
	self.ReadRegister()
	//free play session
	for e := CursorList.Front(); e != nil; {
		switch value1 := e.Value.(type) {
		case *Session:
			cursorSession := value1
			close(cursorSession.PacketAck)
			next = e.Next()
			CursorList.Remove(e)
			e = next
		}
	}
}

func (self *Session) rtmpCloseSessionHanler() {
	self.stage++
	if self.publishing == true {
		self.rtmpClosePublishingSession()
	}else{
		self.rtmpClosePlaySession()
	}

}

func (self *Session) writeRtmpHead() (err error) {
	var metadata amf.AMFMap
	var streams []av.CodecData

	if self.aCodec == nil && self.vCodec == nil {
		return
	}
	if self.aCodec != nil {
		streams = append(streams, self.aCodec)
	}
	if self.vCodec != nil {
		streams = append(streams, self.vCodec)
	}

	if metadata, err = flv.NewMetadataByStreams(streams); err != nil {
		return
	}
	if err = self.writeDataMsg(5, self.avmsgsid, "onMetaData", metadata); err != nil {
		return
	}
	for _, stream := range streams {
		var ok bool
		var tag *flvio.Tag
		if tag, ok, err = self.CodecDataToTag(stream); err != nil {
			return
		}

		if ok {
			if err = self.writeAVTag(tag, 0); err != nil {
				return
			}
		}
	}
	//panic(55)
	return
}

func (self *Session) rtmpSendGop() (err error) {

	if self.GopCache == nil {
		return
	}
	for pkt := self.GopCache.RingBufferGet(); pkt != nil; {
		err = self.writeAVPacket(pkt)
		if err != nil {
			self.GopCache = nil
			return err
		}
		pkt = self.GopCache.RingBufferGet();
	}
	self.GopCache = nil
	return
}

func (self *Session) sendRtmpAvPackets() (err error) {
	for {
		pkt := self.CurQue.RingBufferGet()
		select {
		case <-self.context.Done():
		// here publish may over so play is over
			fmt.Println("the publisher is close")
			self.isClosed = true
			return
		default:

		// 没有结束 ... 执行 ...
		}

		if pkt == nil && self.isClosed  != true {
			select {
			case <-self.PacketAck:
			case <-time.After(time.Second * MAXREADTIMEOUT):
			}
		}
		if self.pubSession.isClosed == true{
			self.isClosed = true
		}
		if pkt != nil {
			if err = self.writeAVPacket(pkt); err != nil {
				return
			}
		}
	}
}

func (self *Session) ServerSession(stage int) (err error) {

	for self.stage <= stage {
		switch self.stage {
		//first handshake
		case stageHandshakeStart:
			if err = self.handshakeServer(); err != nil {
				return
			}
		case stageHandshakeDone:
			if err = self.rtmpReadCmdMsgCycle(); err != nil {
				return
			}
		case stageCommandDone:
			if self.publishing {
				self.context, self.cancel = context.WithCancel(context.Background())
				//only publish and relay need cache gop
				self.GopCache = AvQue.RingBufferCreate(8)
				err = self.rtmpReadMsgCycle()
				self.stage = stageSessionDone
				continue
			} else if self.playing {
				pubSession:= RtmpSessionGet(self.URL.Path)
				if pubSession != nil {
					//register play to the publish
					select {
					case pubSession.RegisterChannel <- self:
					case <-time.After(time.Second * MAXREADTIMEOUT):
					//may be is err
					}
					self.pubSession = pubSession
					//copy gop,codec here all new play Competitive the publishing lock
					pubSession.RLock()
					self.aCodec = pubSession.aCodec
					self.vCodecData = pubSession.vCodecData
					self.aCodecData = pubSession.aCodecData
					self.vCodec = pubSession.vCodec
					//copy all gop just ptr copy
					self.GopCache = pubSession.GopCache.GopCopy()
					pubSession.RUnlock()

					self.context, self.cancel = pubSession.context, pubSession.cancel
					//send audio,video head and meta
					if err = self.writeRtmpHead(); err != nil {
						self.isClosed = true
						return err
					}
					//send gop for first screen
					if err = self.rtmpSendGop(); err != nil {
						self.isClosed = true
						return err
					}
					if err = self.sendRtmpAvPackets(); err != nil {
						self.isClosed = true
						return err
					}
					self.isClosed = true
					self.stage = stageSessionDone
				} else {
					//relay play
				}
			}
		case stageSessionDone:
			//some thing close handler
			self.rtmpCloseSessionHanler()
		}
	}
	return
}