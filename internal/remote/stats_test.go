package remote

import (
	"strings"
	"testing"
	"time"
)

const statsFixture = `@@osat-stat@@
cpu  100 20 30 500 50 10 5 0 0 0
cpu0 50 10 15 250 25 5 2 0 0 0
@@osat-mem@@
MemTotal:        8000000 kB
MemFree:         1000000 kB
MemAvailable:    3000000 kB
Buffers:          200000 kB
Cached:          1000000 kB
SReclaimable:     100000 kB
Shmem:             50000 kB
@@osat-net@@
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0
  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
docker0: 5000 50 0 0 0 0 0 0 6000 60 0 0 0 0 0 0
@@osat-route@@
Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0100000A	0003	0	0	100	00000000	0	0	0
docker0	000011AC	00000000	0001	0	0	0	0000FFFF	0	0	0
@@osat-df@@
Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/sda1         80000000 20000000  60000000      25% /
`

func TestParseLinuxStats(t *testing.T) {
	got, err := parseLinuxStats(statsFixture)
	if err != nil {
		t.Fatal(err)
	}
	if got.cpuTotal != 715 || got.cpuIdle != 550 {
		t.Errorf("CPU = total %d, idle %d; quería 715 y 550", got.cpuTotal, got.cpuIdle)
	}
	if got.memTotal != 8000000*1024 || got.memUsed != 5000000*1024 {
		t.Errorf("memoria = %d/%d", got.memUsed, got.memTotal)
	}
	if got.netRx != 1000 || got.netTx != 2000 {
		t.Errorf("red = %d/%d; quería solo eth0 (1000/2000)", got.netRx, got.netTx)
	}
	if got.diskTotal != 80000000*1024 || got.diskUsed != 20000000*1024 {
		t.Errorf("disco = %d/%d", got.diskUsed, got.diskTotal)
	}
}

func TestParseLinuxStatsWithoutOptionalSections(t *testing.T) {
	out := `@@osat-stat@@
cpu  10 0 10 80 0 0 0 0
@@osat-mem@@
MemTotal:       1000 kB
MemFree:         100 kB
Buffers:          50 kB
Cached:          200 kB
SReclaimable:     50 kB
Shmem:            50 kB
@@osat-net@@
    lo: 10 0 0 0 0 0 0 0 10 0 0 0 0 0 0 0
  eth0: 100 1 0 0 0 0 0 0 200 2 0 0 0 0 0 0
docker0: 300 3 0 0 0 0 0 0 400 4 0 0 0 0 0 0
`
	got, err := parseLinuxStats(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.memTotal != 1000*1024 || got.memUsed != 650*1024 {
		t.Errorf("memoria sin MemAvailable = %d/%d; quería 650/1000 kB", got.memUsed, got.memTotal)
	}
	if got.netRx != 400 || got.netTx != 600 {
		t.Errorf("red sin ruta = %d/%d; quería la suma sin loopback (400/600)", got.netRx, got.netTx)
	}
	if got.diskTotal != 0 || got.diskUsed != 0 {
		t.Errorf("disco sin df = %d/%d; quería cero", got.diskUsed, got.diskTotal)
	}
}

func TestParseLinuxStatsNeedsProc(t *testing.T) {
	if _, err := parseLinuxStats("@@osat-mem@@\nMemTotal: 1 kB\n"); err == nil {
		t.Fatal("sin /proc/stat tendría que fallar")
	}
	if _, err := parseLinuxStats("@@osat-stat@@\ncpu  1 2 3 4\n"); err == nil {
		t.Fatal("sin /proc/meminfo tendría que fallar")
	}
}

func TestStatsSectionsIgnoresCRLF(t *testing.T) {
	sec := statsSections("@@osat-stat@@\r\ncpu  1 2 3 4\r\n@@osat-mem@@\r\nMemTotal: 1 kB\r\n")
	if !strings.Contains(sec["stat"], "cpu  1 2 3 4") || !strings.Contains(sec["mem"], "MemTotal: 1 kB") {
		t.Fatalf("secciones = %#v", sec)
	}
}

func TestStatsRates(t *testing.T) {
	var st statsState
	t0 := time.Unix(1000, 0)
	cpu, rx, tx := st.rates(linuxSample{cpuTotal: 1000, cpuIdle: 800, netRx: 1000, netTx: 500}, t0)
	if cpu != nil || rx != 0 || tx != 0 {
		t.Fatalf("primera muestra = %v, %d, %d; quería nil, 0, 0", cpu, rx, tx)
	}

	cpu, rx, tx = st.rates(linuxSample{cpuTotal: 1200, cpuIdle: 900, netRx: 3000, netTx: 1500}, t0.Add(2*time.Second))
	if cpu == nil || *cpu != 50 || rx != 1000 || tx != 500 {
		t.Fatalf("segunda muestra = %v, %d, %d; quería 50%%, 1000, 500", cpu, rx, tx)
	}

	// Dos pedidos muy seguidos no recalculan con una ventana diminuta.
	cpu, rx, tx = st.rates(linuxSample{cpuTotal: 9999, cpuIdle: 1, netRx: 99999, netTx: 99999}, t0.Add(2100*time.Millisecond))
	if cpu == nil || *cpu != 50 || rx != 1000 || tx != 500 {
		t.Fatalf("muestra seguida = %v, %d, %d; quería la anterior", cpu, rx, tx)
	}

	// Después de mucho tiempo (pestaña oculta) se toma una base nueva.
	cpu, rx, tx = st.rates(linuxSample{cpuTotal: 20000, cpuIdle: 19000, netRx: 200000, netTx: 100000}, t0.Add(40*time.Second))
	if cpu != nil || rx != 0 || tx != 0 {
		t.Fatalf("muestra vieja = %v, %d, %d; quería nil, 0, 0", cpu, rx, tx)
	}
	base := st.prev
	cpu, rx, tx = st.rates(linuxSample{cpuTotal: 20200, cpuIdle: 19100, netRx: 201000, netTx: 100500}, base.at.Add(time.Second))
	if cpu == nil || *cpu != 50 || rx != 1000 || tx != 500 {
		t.Fatalf("tras reiniciar la base = %v, %d, %d", cpu, rx, tx)
	}
}

func TestStatsRatesCountersReset(t *testing.T) {
	var st statsState
	t0 := time.Unix(2000, 0)
	st.rates(linuxSample{cpuTotal: 5000, cpuIdle: 4000, netRx: 5000, netTx: 5000}, t0)
	cpu, rx, tx := st.rates(linuxSample{cpuTotal: 100, cpuIdle: 80, netRx: 10, netTx: 10}, t0.Add(time.Second))
	if cpu == nil || *cpu != 0 || rx != 0 || tx != 0 {
		t.Fatalf("tras reinicio = %v, %d, %d; quería 0, 0, 0", cpu, rx, tx)
	}
}
