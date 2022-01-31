package plugin_summary

import (
	"github.com/gogf/gf/text/gregex"
	"log"
	"net/http"
	"strings"
	"time"

	. "github.com/Monibuca/engine/v3"
	. "github.com/Monibuca/utils/v3"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/net"
)

// Summary 系统摘要数据
var Summary ServerSummary
var config = struct {
	SampleRate int
	NetAdapter string //在容器化设备会有很多无效的虚拟网卡，只读取有意义的网卡
}{1, ""}

func init() {
	plugin := PluginConfig{
		Name:   "Summary",
		Config: &config,
		HotConfig: map[string]func(interface{}){
			"NetAdapter": func(v interface{}) {
				config.NetAdapter = v.(string)
			},
		},
	}
	plugin.Install(Summary.StartSummary)
	http.HandleFunc("/api/summary", summary)
}
func summary(w http.ResponseWriter, r *http.Request) {
	CORS(w, r)
	sse := NewSSE(w, r.Context())
	Summary.Add()
	defer Summary.Done()
	sse.WriteJSON(&Summary)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := sse.WriteJSON(&Summary); err != nil {
				log.Println(err)
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// ServerSummary 系统摘要定义
type ServerSummary struct {
	Address string
	Memory  struct {
		Total uint64
		Free  uint64
		Used  uint64
		Usage float64
	}
	CPUUsage float64
	HardDisk struct {
		Total uint64
		Free  uint64
		Used  uint64
		Usage float64
	}
	NetWork     []NetWorkInfo
	Streams     []*Stream
	lastNetWork []NetWorkInfo
	ref         int
	control     chan bool
	reportChan  chan *ServerSummary
	Children    map[string]*ServerSummary
}

// NetWorkInfo 网速信息
type NetWorkInfo struct {
	Name         string
	Receive      uint64
	Sent         uint64
	ReceiveSpeed uint64
	SentSpeed    uint64
}

//StartSummary 开始定时采集数据，每秒一次
func (s *ServerSummary) StartSummary() {
	ticker := time.NewTicker(time.Second * time.Duration(config.SampleRate))
	s.control = make(chan bool)
	s.reportChan = make(chan *ServerSummary)
	for {
		select {
		case <-ticker.C:
			if s.ref > 0 {
				Summary.collect()
			}
		case v := <-s.control:
			if v {
				if s.ref++; s.ref == 1 {
					log.Println("start report summary")
					TriggerHook("Summary", true)
				}
			} else {
				if s.ref--; s.ref == 0 {
					s.lastNetWork = nil
					log.Println("stop report summary")
					TriggerHook("Summary", false)
				}
			}
		case report := <-s.reportChan:
			s.Children[report.Address] = report
		}
	}
}

// Running 是否正在采集数据
func (s *ServerSummary) Running() bool {
	return s.ref > 0
}

// Add 增加订阅者
func (s *ServerSummary) Add() {
	s.control <- true
}

// Done 删除订阅者
func (s *ServerSummary) Done() {
	s.control <- false
}

// Report 上报数据
func (s *ServerSummary) Report(slave *ServerSummary) {
	s.reportChan <- slave
}
func (s *ServerSummary) collect() {
	v, _ := mem.VirtualMemory()
	d, _ := disk.Usage("/")
	nv, _ := net.IOCounters(true)

	s.Memory.Total = v.Total / 1024 / 1024
	s.Memory.Free = v.Available / 1024 / 1024
	s.Memory.Used = v.Used / 1024 / 1024
	s.Memory.Usage = v.UsedPercent

	if cc, _ := cpu.Percent(time.Second, false); len(cc) > 0 {
		s.CPUUsage = cc[0]
	}
	s.HardDisk.Free = d.Free / 1024 / 1024 / 1024
	s.HardDisk.Total = d.Total / 1024 / 1024 / 1024
	s.HardDisk.Used = d.Used / 1024 / 1024 / 1024
	s.HardDisk.Usage = d.UsedPercent
	s.NetWork = make([]NetWorkInfo, len(nv))
	for i, n := range nv {
		if !isNetAdapter(n.Name) {
			continue
		}

		s.NetWork[i].Name = n.Name
		s.NetWork[i].Receive = n.BytesRecv
		s.NetWork[i].Sent = n.BytesSent
		if s.lastNetWork != nil && len(s.lastNetWork) > i {
			s.NetWork[i].ReceiveSpeed = n.BytesRecv - s.lastNetWork[i].Receive
			s.NetWork[i].SentSpeed = n.BytesSent - s.lastNetWork[i].Sent
		}
	}
	s.lastNetWork = s.NetWork
	s.Streams = Streams.ToList()
	return
}

//NetAdapter 通过匹配和正则判断要过滤的无效网卡
func isNetAdapter(name string) bool {
	if name == "" {
		return true
	}

	//正则用@作正则修饰符
	if !strings.Contains(config.NetAdapter, "@") {
		return strings.Contains(name, config.NetAdapter)
	}

	return gregex.IsMatchString(strings.Trim(config.NetAdapter, "@"), name)
}
