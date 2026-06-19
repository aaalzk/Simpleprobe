package agent

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Metrics holds all collected system metrics for a single snapshot.
type Metrics struct {
	CPUPercent    float64            `json:"cpu_percent"`
	MemTotal      uint64             `json:"mem_total"`    // bytes
	MemUsed       uint64             `json:"mem_used"`     // bytes
	MemPercent    float64            `json:"mem_percent"`
	DiskTotal     uint64             `json:"disk_total"`   // bytes
	DiskUsed      uint64             `json:"disk_used"`    // bytes
	DiskPercent   float64            `json:"disk_percent"`
	NetRxBytes    uint64             `json:"net_rx_bytes"` // cumulative
	NetTxBytes    uint64             `json:"net_tx_bytes"` // cumulative
	NetRxRate     float64            `json:"net_rx_rate"`  // bytes/s
	NetTxRate     float64            `json:"net_tx_rate"`  // bytes/s
	Load1         float64            `json:"load_1"`
	Load5         float64            `json:"load_5"`
	Load15        float64            `json:"load_15"`
	Uptime        uint64             `json:"uptime"`       // seconds
	TCPConns      uint64             `json:"tcp_conns"`
	ProcessCount  uint64             `json:"process_count"`
	TopCPUProcs   []ProcInfo         `json:"top_cpu_procs"`
	NICs          map[string]NICStat `json:"nics"`
	OSName        string             `json:"os_name"`
	KernelVersion string             `json:"kernel_version"`
}

// NICStat holds per-interface network statistics.
type NICStat struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// ProcInfo holds information about a single process for top-N reporting.
type ProcInfo struct {
	Name       string  `json:"name"`
	PID        int     `json:"pid"`
	CPUPercent float64 `json:"cpu_percent"`
}

// Report is the payload sent to the server.
type Report struct {
	Name      string  `json:"name"`
	Timestamp int64   `json:"timestamp"`
	Metrics   Metrics `json:"metrics"`
}

// Collector gathers system metrics from /proc on Linux.
type Collector struct {
	prevCPU      cpuSample
	prevNet      map[string]nicSample
	prevNetTime  time.Time
	prevNetBytes uint64 // total across all nics
	prevProcs    map[int]procCPUSample
	prevProcTime time.Time
	osName       string
	kernelVer    string
}

type cpuSample struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
	steal   uint64
}

type nicSample struct {
	rx uint64
	tx uint64
}

type procCPUSample struct {
	utime uint64
	stime uint64
}

// NewCollector creates a new Collector and performs one-time lookups.
func NewCollector() *Collector {
	c := &Collector{}
	c.osName = c.readOSName()
	c.kernelVer = c.readKernelVersion()
	// Take an initial CPU sample so the first delta is meaningful.
	c.prevCPU = c.readCPUSample()
	c.prevNet, _, _ = c.readNICSample()
	c.prevNetTime = time.Now()
	c.prevProcs = c.readProcSamples()
	c.prevProcTime = time.Now()
	return c
}

// Collect gathers all current metrics into a Report.
func (c *Collector) Collect(name string) Report {
	now := time.Now()

	// CPU delta
	cpuPct, curCPU := c.cpuPercent(c.prevCPU)
	c.prevCPU = curCPU

	// Memory
	memTotal, memUsed, memPct := c.memoryInfo()

	// Disk
	diskTotal, diskUsed, diskPct := c.diskInfo()

	// Network
	curNet, totalRx, totalTx := c.readNICSample()
	netRxRate, netTxRate := c.netRate(c.prevNet, c.prevNetBytes, curNet, totalRx+totalTx, c.prevNetTime, now)
	c.prevNet = curNet
	c.prevNetBytes = totalRx + totalTx
	c.prevNetTime = now

	// Load
	l1, l5, l15 := c.loadAvg()

	// Uptime
	upt := c.uptime()

	// Misc
	tcpConns := c.tcpConnCount()
	procCount := c.processCount()

	// Top CPU processes
	topCPU := c.collectTopCPU(3)

	// Build NIC map
	nics := make(map[string]NICStat)
	for iface, s := range curNet {
		nics[iface] = NICStat{RxBytes: s.rx, TxBytes: s.tx}
	}

	return Report{
		Name:      name,
		Timestamp: now.Unix(),
		Metrics: Metrics{
			CPUPercent:    cpuPct,
			MemTotal:      memTotal,
			MemUsed:       memUsed,
			MemPercent:    memPct,
			DiskTotal:     diskTotal,
			DiskUsed:      diskUsed,
			DiskPercent:   diskPct,
			NetRxBytes:    totalRx,
			NetTxBytes:    totalTx,
			NetRxRate:     netRxRate,
			NetTxRate:     netTxRate,
			Load1:         l1,
			Load5:         l5,
			Load15:        l15,
			Uptime:        upt,
			TCPConns:      tcpConns,
			ProcessCount:  procCount,
			TopCPUProcs:   topCPU,
			NICs:          nics,
			OSName:        c.osName,
			KernelVersion: c.kernelVer,
		},
	}
}

// --- /proc readers ---

func (c *Collector) readCPUSample() cpuSample {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			break
		}
		return cpuSample{
			user:    parseUint(fields[1]),
			nice:    parseUint(fields[2]),
			system:  parseUint(fields[3]),
			idle:    parseUint(fields[4]),
			iowait:  parseUint(fields[5]),
			irq:     parseUint(fields[6]),
			softirq: parseUint(fields[7]),
			steal:   parseUintSafe(fields, 8),
		}
	}
	return cpuSample{}
}

func (c *Collector) cpuPercent(prev cpuSample) (float64, cpuSample) {
	cur := c.readCPUSample()
	prevTotal := prev.user + prev.nice + prev.system + prev.idle + prev.iowait + prev.irq + prev.softirq + prev.steal
	curTotal := cur.user + cur.nice + cur.system + cur.idle + cur.iowait + cur.irq + cur.softirq + cur.steal
	totalDelta := curTotal - prevTotal
	idleDelta := cur.idle + cur.iowait - prev.idle - prev.iowait
	if totalDelta == 0 {
		return 0, cur
	}
	pct := (1.0 - float64(idleDelta)/float64(totalDelta)) * 100.0
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, cur
}

func (c *Collector) memoryInfo() (total, used uint64, pct float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0
	}
	defer f.Close()
	var memTotal, memFree, buffers, cached, sReclaimable uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal = parseUint(fields[1]) * 1024
		case "MemFree:":
			memFree = parseUint(fields[1]) * 1024
		case "Buffers:":
			buffers = parseUint(fields[1]) * 1024
		case "Cached:":
			cached = parseUint(fields[1]) * 1024
		case "SReclaimable:":
			sReclaimable = parseUint(fields[1]) * 1024
		}
	}
	if memTotal == 0 {
		return 0, 0, 0
	}
	used = memTotal - memFree - buffers - cached - sReclaimable
	pct = float64(used) / float64(memTotal) * 100.0
	return memTotal, used, pct
}

func (c *Collector) diskInfo() (total, used uint64, pct float64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, 0, 0
	}
	total = stat.Blocks * uint64(stat.Bsize)
	avail := stat.Bavail * uint64(stat.Bsize)
	used = total - avail
	if total > 0 {
		pct = float64(used) / float64(total) * 100.0
	}
	return
}

func (c *Collector) readNICSample() (map[string]nicSample, uint64, uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, 0, 0
	}
	defer f.Close()
	nics := make(map[string]nicSample)
	var totalRx, totalTx uint64
	sc := bufio.NewScanner(f)
	// Skip the two header lines
	sc.Scan()
	sc.Scan()
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Format: iface: rx_bytes rx_packets ... tx_bytes ...
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}
		rx := parseUint(fields[0])
		tx := parseUint(fields[8])
		nics[iface] = nicSample{rx: rx, tx: tx}
		totalRx += rx
		totalTx += tx
	}
	return nics, totalRx, totalTx
}

func (c *Collector) netRate(prev map[string]nicSample, prevTotal uint64, cur map[string]nicSample, curTotal uint64, prevTime, curTime time.Time) (float64, float64) {
	elapsed := curTime.Sub(prevTime).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}
	// Calculate rate based on total across all interfaces
	delta := float64(curTotal) - float64(prevTotal)
	if delta < 0 {
		delta = 0
	}
	// We can't separate Rx/Tx rate from the total easily, so we approximate
	// by looking at the delta of each interface
	var rxDelta, txDelta float64
	for iface, cs := range cur {
		ps, ok := prev[iface]
		if !ok {
			continue
		}
		rxD := float64(cs.rx) - float64(ps.rx)
		txD := float64(cs.tx) - float64(ps.tx)
		if rxD > 0 {
			rxDelta += rxD
		}
		if txD > 0 {
			txDelta += txD
		}
	}
	return rxDelta / elapsed, txDelta / elapsed
}

func (c *Collector) loadAvg() (float64, float64, float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	return parseFloat(fields[0]), parseFloat(fields[1]), parseFloat(fields[2])
}

func (c *Collector) uptime() uint64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}
	return uint64(parseFloat(fields[0]))
}

func (c *Collector) tcpConnCount() uint64 {
	var count uint64
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // skip header
		for sc.Scan() {
			count++
		}
		f.Close()
	}
	return count
}

func (c *Collector) processCount() uint64 {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var count uint64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Check if directory name is a number (PID)
		if _, err := strconv.ParseUint(e.Name(), 10, 64); err == nil {
			count++
		}
	}
	return count
}

// readProcSamples reads CPU time for all processes from /proc/[pid]/stat.
// Returns a map of PID -> procCPUSample for delta calculation.
func (c *Collector) readProcSamples() map[int]procCPUSample {
	procs := make(map[int]procCPUSample)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return procs
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		utime, stime := c.readProcCPU(pid)
		procs[pid] = procCPUSample{utime: utime, stime: stime}
	}
	return procs
}

// readProcCPU reads utime and stime from /proc/[pid]/stat.
func (c *Collector) readProcCPU(pid int) (uint64, uint64) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0
	}
	// Format: pid (comm) state ppid pgrp session tty_nr tpgid flags minflt cminflt
	// majflt cmajflt utime stime ...
	// The comm field may contain spaces and parentheses, so find the last ')'
	content := string(data)
	lastParen := strings.LastIndex(content, ")")
	if lastParen < 0 {
		return 0, 0
	}
	fields := strings.Fields(content[lastParen+1:])
	// After the last ')', the fields are:
	// [0]=state [1]=ppid ... [11]=utime [12]=stime
	if len(fields) < 13 {
		return 0, 0
	}
	return parseUint(fields[11]), parseUint(fields[12])
}

// readProcName reads the process name from /proc/[pid]/cmdline, falling back
// to the comm field from /proc/[pid]/stat.
func (c *Collector) readProcName(pid int) string {
	// Try cmdline first for the full command name
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err == nil && len(data) > 0 {
		// cmdline uses null bytes as separators
		name := strings.ReplaceAll(string(data), "\x00", " ")
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	// Fallback to comm from stat
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return fmt.Sprintf("PID %d", pid)
	}
	content := string(statData)
	start := strings.Index(content, "(")
	end := strings.LastIndex(content, ")")
	if start >= 0 && end > start {
		return content[start+1 : end]
	}
	return fmt.Sprintf("PID %d", pid)
}

// collectTopCPU returns the top N processes by CPU usage.
func (c *Collector) collectTopCPU(topN int) []ProcInfo {
	now := time.Now()
	curProcs := c.readProcSamples()

	elapsed := now.Sub(c.prevProcTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	type procDelta struct {
		pid  int
		name string
		pct  float64
	}
	var deltas []procDelta

	for pid, cur := range curProcs {
		prev, ok := c.prevProcs[pid]
		if !ok {
			continue // new process, skip first sample
		}
		cpuDelta := (cur.utime + cur.stime) - (prev.utime + prev.stime)
		// CLK_TCK = 100 on Linux: CPU% = delta / (elapsed * clk_tck) * 100
		pct := float64(cpuDelta) / (elapsed * 100) * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 0 {
			deltas = append(deltas, procDelta{pid: pid, pct: pct})
		}
	}

	sort.Slice(deltas, func(i, j int) bool {
		return deltas[i].pct > deltas[j].pct
	})

	if len(deltas) > topN {
		deltas = deltas[:topN]
	}

	// Resolve names only for the top N
	result := make([]ProcInfo, len(deltas))
	for i, d := range deltas {
		result[i] = ProcInfo{
			Name:       c.readProcName(d.pid),
			PID:        d.pid,
			CPUPercent: d.pct,
		}
	}

	c.prevProcs = curProcs
	c.prevProcTime = now
	return result
}

func (c *Collector) readOSName() string {
	// Try /etc/os-release first
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	// Fallback: try lsb_release
	out, err := exec.Command("lsb_release", "-d", "-s").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return "Linux"
}

func (c *Collector) readKernelVersion() string {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "unknown"
	}
	// Format: Linux version 5.15.0-xxx ...
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fields[0] + " " + fields[2]
	}
	return "Linux"
}

// --- helpers ---

func parseUint(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func parseUintSafe(fields []string, idx int) uint64 {
	if idx < len(fields) {
		return parseUint(fields[idx])
	}
	return 0
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// FormatDuration formats seconds into a human-readable duration string.
func FormatDuration(seconds uint64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		h := seconds / 3600
		m := (seconds % 3600) / 60
		return fmt.Sprintf("%dh%dm", h, m)
	}
	d := seconds / 86400
	h := (seconds % 86400) / 3600
	return fmt.Sprintf("%dd%dh", d, h)
}
