package remote

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Recursos del servidor para la barra que se muestra arriba de las terminales
// SSH. Solo Linux: se leen /proc y df por un canal de ejecución aparte, sin
// escribir nada en la shell del usuario (no ensucia el historial ni compite con
// lo que esté corriendo en pantalla).

const (
	statsTimeout   = 4 * time.Second
	statsMaxOutput = 128 << 10
	statsLinux     = "linux"
	statsNo        = "unsupported"
)

// linuxStatsCommand imprime una marca y el contenido de cada archivo. sh -c
// evita depender de la shell del usuario; las marcas separan las secciones.
const linuxStatsCommand = `sh -c 'echo @@osat-stat@@; cat /proc/stat; ` +
	`echo @@osat-mem@@; cat /proc/meminfo; ` +
	`echo @@osat-net@@; cat /proc/net/dev; ` +
	`echo @@osat-route@@; cat /proc/net/route 2>/dev/null; ` +
	`echo @@osat-df@@; LC_ALL=C df -Pk / 2>/dev/null'`

// SessionStats es una muestra de los recursos del servidor. Supported queda en
// false en terminales locales, mongosh o servidores que no son Linux.
type SessionStats struct {
	Supported  bool     `json:"supported"`
	CPUPercent *float64 `json:"cpuPercent,omitempty"`
	MemUsed    uint64   `json:"memUsed"`
	MemTotal   uint64   `json:"memTotal"`
	DiskUsed   uint64   `json:"diskUsed"`
	DiskTotal  uint64   `json:"diskTotal"`
	NetRxBps   uint64   `json:"netRxBps"`
	NetTxBps   uint64   `json:"netTxBps"`
}

// linuxSample son los contadores acumulados de una lectura. CPU y red se
// convierten en porcentaje/tasa comparando con la muestra anterior.
type linuxSample struct {
	cpuTotal  uint64
	cpuIdle   uint64
	memUsed   uint64
	memTotal  uint64
	diskUsed  uint64
	diskTotal uint64
	netRx     uint64
	netTx     uint64
}

type statsSample struct {
	at time.Time
	linuxSample
}

// statsState guarda lo mínimo por sesión: si el servidor es Linux y la última
// lectura, para calcular las diferencias. El mutex también evita dos comandos
// a la vez si el frontend pidiera dos muestras seguidas.
type statsState struct {
	mu      sync.Mutex
	os      string // "", statsLinux o statsNo
	prev    *statsSample
	lastCPU *float64
	lastRx  uint64
	lastTx  uint64
}

// SessionStats lee los recursos de la sesión. En terminales que no son SSH a
// Linux devuelve Supported=false sin error; los errores de red o de exec se
// devuelven para que el frontend reintente y, si persisten, oculte la barra.
func (m *Manager) SessionStats(id string) (SessionStats, error) {
	s, err := m.get(id)
	if err != nil {
		return SessionStats{}, err
	}
	if s.shared == nil || s.mongo || s.noShell.Load() {
		return SessionStats{Supported: false}, nil
	}

	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	if s.stats.os == statsNo {
		return SessionStats{Supported: false}, nil
	}

	var out SessionStats
	err = m.withClient(id, func(c *ssh.Client) error {
		if s.stats.os == "" {
			os, err := detectLinux(c)
			if err != nil {
				return err
			}
			s.stats.os = os
		}
		if s.stats.os != statsLinux {
			return nil
		}
		raw, err := runSSHCommand(c, linuxStatsCommand, statsTimeout)
		if err != nil {
			return err
		}
		sample, err := parseLinuxStats(raw)
		if err != nil {
			return err
		}
		cpu, rx, tx := s.stats.rates(sample, time.Now())
		out = SessionStats{
			Supported:  true,
			CPUPercent: cpu,
			MemUsed:    sample.memUsed,
			MemTotal:   sample.memTotal,
			DiskUsed:   sample.diskUsed,
			DiskTotal:  sample.diskTotal,
			NetRxBps:   rx,
			NetTxBps:   tx,
		}
		return nil
	})
	if err != nil {
		return SessionStats{}, err
	}
	if s.stats.os != statsLinux {
		return SessionStats{Supported: false}, nil
	}
	return out, nil
}

// detectLinux evita mandar comandos de /proc a equipos de red o a Windows. Si
// uname existe pero falla, el servidor no es un Unix que nos sirva: se marca
// como no soportado. Un error de transporte se devuelve para reintentar.
func detectLinux(c *ssh.Client) (string, error) {
	if strings.Contains(strings.ToLower(string(c.ServerVersion())), "windows") {
		return statsNo, nil
	}
	out, err := runSSHCommand(c, "uname -s", statsTimeout)
	if err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return statsNo, nil
		}
		return "", err
	}
	if strings.EqualFold(strings.TrimSpace(out), "Linux") {
		return statsLinux, nil
	}
	return statsNo, nil
}

// runSSHCommand ejecuta un comando fijo en un canal nuevo y devuelve su salida.
// stderr se descarta y la salida se limita para que un servidor raro no llene
// la memoria; el timeout cierra el canal.
func runSSHCommand(c *ssh.Client, cmd string, timeout time.Duration) (string, error) {
	sess, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	var out limitedBuffer
	out.max = statsMaxOutput
	sess.Stdout = &out
	sess.Stderr = io.Discard
	if err := sess.Start(cmd); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
		return out.String(), nil
	case <-timer.C:
		_ = sess.Close()
		return "", errors.New("el servidor tardó demasiado en responder")
	}
}

// parseLinuxStats separa las secciones y arma una muestra. stat y meminfo son
// obligatorios; red y disco pueden faltar (se muestran como cero).
func parseLinuxStats(out string) (linuxSample, error) {
	sec := statsSections(out)
	cpuTotal, cpuIdle, ok := parseCPU(sec["stat"])
	if !ok {
		return linuxSample{}, errors.New("no se pudo leer /proc/stat")
	}
	memUsed, memTotal := parseMeminfo(sec["mem"])
	if memTotal == 0 {
		return linuxSample{}, errors.New("no se pudo leer /proc/meminfo")
	}
	rx, tx := parseNetDev(sec["net"], sec["route"])
	diskUsed, diskTotal := parseDF(sec["df"])
	return linuxSample{
		cpuTotal:  cpuTotal,
		cpuIdle:   cpuIdle,
		memUsed:   memUsed,
		memTotal:  memTotal,
		diskUsed:  diskUsed,
		diskTotal: diskTotal,
		netRx:     rx,
		netTx:     tx,
	}, nil
}

func statsSections(out string) map[string]string {
	sec := map[string]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "@@osat-") && strings.HasSuffix(line, "@@") {
			cur = strings.TrimSuffix(strings.TrimPrefix(line, "@@osat-"), "@@")
			continue
		}
		if cur != "" {
			sec[cur] += line + "\n"
		}
	}
	return sec
}

func parseCPU(s string) (total, idle uint64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return 0, 0, false
		}
		vals := make([]uint64, 0, len(fields)-1)
		for _, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			vals = append(vals, v)
		}
		// user nice system idle iowait irq softirq steal; guest/guest_nice ya
		// están incluidos en user/nice y no se suman.
		n := len(vals)
		if n > 8 {
			n = 8
		}
		for i := 0; i < n; i++ {
			total += vals[i]
		}
		idle = vals[3]
		if n > 4 {
			idle += vals[4]
		}
		if idle > total {
			idle = total
		}
		return total, idle, total > 0
	}
	return 0, 0, false
}

func parseMeminfo(s string) (used, total uint64) {
	vals := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSpace(key)] = v * 1024
	}
	total = vals["MemTotal"]
	if total == 0 {
		return 0, 0
	}
	if available, ok := vals["MemAvailable"]; ok && available <= total {
		return total - available, total
	}
	free := vals["MemFree"] + vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	if shm := vals["Shmem"]; shm < free {
		free -= shm
	}
	if free > total {
		free = total
	}
	return total - free, total
}

func parseDF(s string) (used, total uint64) {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "Filesystem") {
			continue
		}
		if i+1 >= len(lines) {
			return 0, 0
		}
		fields := strings.Fields(lines[i+1])
		if len(fields) < 3 {
			return 0, 0
		}
		blocks, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || blocks == 0 {
			return 0, 0
		}
		usedBlocks, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return 0, 0
		}
		return usedBlocks * 1024, blocks * 1024
	}
	return 0, 0
}

func defaultInterface(route string) string {
	for _, line := range strings.Split(route, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

// parseNetDev suma la interfaz de la ruta por defecto; si no se pudo leer la
// ruta, suma todas las que no son loopback (puede contar de más si hay bridges).
func parseNetDev(dev, route string) (rx, tx uint64) {
	iface := defaultInterface(route)
	for _, line := range strings.Split(dev, "\n") {
		name, data, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		if name == "" || name == "lo" {
			continue
		}
		if iface != "" && name != iface {
			continue
		}
		fields := strings.Fields(data)
		if len(fields) < 9 {
			continue
		}
		r, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		t, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			continue
		}
		rx += r
		tx += t
	}
	return rx, tx
}

// rates convierte los contadores acumulados en CPU % y bytes/s. La primera
// muestra no tiene con qué comparar y devuelve CPU nil. Si pasó demasiado
// tiempo (pestaña oculta), se toma una base nueva para no promediar horas.
func (st *statsState) rates(curr linuxSample, now time.Time) (cpu *float64, rx, tx uint64) {
	if st.prev == nil {
		st.prev = &statsSample{at: now, linuxSample: curr}
		return nil, 0, 0
	}
	elapsed := now.Sub(st.prev.at)
	if elapsed < 500*time.Millisecond {
		return st.lastCPU, st.lastRx, st.lastTx
	}
	if elapsed > 30*time.Second {
		st.prev = &statsSample{at: now, linuxSample: curr}
		st.lastCPU, st.lastRx, st.lastTx = nil, 0, 0
		return nil, 0, 0
	}

	prev := st.prev
	st.prev = &statsSample{at: now, linuxSample: curr}
	if curr.cpuTotal > prev.cpuTotal {
		dt := curr.cpuTotal - prev.cpuTotal
		var di uint64
		if curr.cpuIdle >= prev.cpuIdle {
			di = curr.cpuIdle - prev.cpuIdle
		}
		if di < dt {
			v := 100 * float64(dt-di) / float64(dt)
			st.lastCPU = &v
		} else {
			v := 0.0
			st.lastCPU = &v
		}
	} else {
		v := 0.0
		st.lastCPU = &v
	}
	if curr.netRx >= prev.netRx {
		rx = uint64(float64(curr.netRx-prev.netRx) / elapsed.Seconds())
	}
	if curr.netTx >= prev.netTx {
		tx = uint64(float64(curr.netTx-prev.netTx) / elapsed.Seconds())
	}
	st.lastRx, st.lastTx = rx, tx
	return st.lastCPU, rx, tx
}
