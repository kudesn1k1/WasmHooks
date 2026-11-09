// Command demo-m2 shows Milestone 2 live, in five scenes, for an audience
// that does not know the code: a shop whose script hangs, first without and
// then with admission control, a sale with a 30-fold flood of calls held by
// a tenant quota, and an operator raising that quota without a restart. Each
// scene prints one line of numbers per second and a summary; a table of all
// scenes closes the demo.
//
// The data plane is the real binary, limited to 4 Ps (GOMAXPROCS=4) like the
// 4 vCPU reference server, fed by a stand-in control plane on the real
// long-poll protocol, so the demo needs no Docker.
//
// Usage (from dataplane/):
//
//	go run ./cmd/demo-m2          # pauses before each scene: press Enter
//	go run ./cmd/demo-m2 -auto    # no pauses, for a rehearsal
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	apiKey      = "whk_demo_m2"
	hookName    = "checkout.discount"
	hookTimeout = 50 * time.Millisecond
	shopA       = "shop-a"
	shopB       = "shop-b"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "demo-m2:", err)
		os.Exit(1)
	}
}

type demo struct {
	out    io.Writer
	in     *bufio.Reader
	auto   bool
	length time.Duration // one scene

	exe, modulesDir string
	cp              *controlPlane
	procs           int
	st              stats
	dp              *dataPlane
	results         []result
}

type result struct {
	scene string
	w     window
	flood bool // shop B was flooding rather than hanging
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("demo-m2", flag.ContinueOnError)
	auto := fs.Bool("auto", false, "run without pauses")
	length := fs.Duration("scene", 8*time.Second, "length of one scene")
	procs := fs.Int("procs", 4, "GOMAXPROCS of the data plane: 4 is the reference server")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d := &demo{out: stdout, in: bufio.NewReader(stdin), auto: *auto, length: *length, procs: *procs}

	tmp, err := os.MkdirTemp("", "wasmhooks-demo-m2-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	d.say("Собираю data plane...")
	if d.exe, d.modulesDir, err = prepare(tmp); err != nil {
		return err
	}
	if d.cp, err = startControlPlane("demo-internal-token"); err != nil {
		return err
	}
	defer d.cp.close()
	defer func() {
		if d.dp != nil {
			d.dp.stop()
		}
	}()

	d.intro()
	off := []string{"-exec-slots", "1000", "-exec-tenant-share", "1000", "-exec-queue", "4000"}
	broken := scene{bModule: modules.loop, bConcurrency: 1024}

	// 1. A normal day, the platform as it was before M2.
	d.cp.set(scene{bModule: modules.discount, bConcurrency: 1024})
	if err := d.restart(off); err != nil {
		return err
	}
	d.pause("Сцена 1. Обычный день. Защиты M2 нет, как было до неё. Магазин А принимает 100 заказов в секунду.")
	d.scene("1. Обычный день", false, func(ctx context.Context, wg *sync.WaitGroup) {
		d.shopATraffic(ctx, wg)
	}, nil)

	// 2. Shop B deploys a script that hangs; no protection.
	d.cp.set(broken)
	d.pause("Сцена 2. Магазин Б выкатил скрипт с бесконечным циклом, 16 его покупателей жмут «оформить».\n" +
		"         Защиты по-прежнему нет. Смотрите на магазин А: он ни при чём.")
	d.scene("2. Скрипт Б завис, без защиты", false, func(ctx context.Context, wg *sync.WaitGroup) {
		d.shopATraffic(ctx, wg)
		buyers(ctx, wg, newCaller(d.dp.base, 64), shopB, 16, d.st.shopB)
	}, nil)

	// 3. The same with M2's running slots.
	if err := d.restart(nil); err != nil {
		return err
	}
	d.pause("Сцена 3. Всё то же самое, но data plane перезапущен с защитой M2 (слоты исполнения по умолчанию).")
	d.scene("3. То же, с защитой M2", false, func(ctx context.Context, wg *sync.WaitGroup) {
		d.shopATraffic(ctx, wg)
		buyers(ctx, wg, newCaller(d.dp.base, 64), shopB, 16, d.st.shopB)
	}, nil)

	// 4. A sale at shop B: a 30-fold flood against a quota of 50/s.
	d.cp.set(scene{bModule: modules.discount, bRateRPS: 50, bConcurrency: 64})
	d.pause("Сцена 4. Скрипт Б исправлен, но у Б распродажа: 1 500 заказов в секунду при квоте 50 в секунду.")
	flood := func(ctx context.Context, wg *sync.WaitGroup) {
		d.shopATraffic(ctx, wg)
		steady(ctx, wg, newCaller(d.dp.base, 256), shopB, 1500, func(o string, _ time.Duration) { d.st.shopB(o) })
	}
	d.scene("4. Распродажа у Б, квота 50/с", true, flood, nil)

	// 5. The operator raises the quota while the flood goes on.
	pid := d.dp.pid()
	d.pause("Сцена 5. Та же распродажа. На 3-й секунде оператор поднимает квоту Б до 500 в секунду,\n" +
		"         data plane не перезапускается.")
	var delivered bool
	d.scene("5. Квота Б поднята до 500/с", true, flood, func(sec int) {
		if sec == 3 {
			raised := time.Now()
			d.cp.set(scene{bModule: modules.discount, bRateRPS: 500, bConcurrency: 64})
			_, delivered = d.cp.waitDelivered(raised, 5*time.Second)
			d.say("     ── оператор поднял квоту Б до 500/с ──")
		}
	})
	if delivered {
		// The data plane holds a long-poll open, so the snapshot leaves the
		// moment it changes; the delay is below what the clock can show.
		d.say(fmt.Sprintf("Data plane получил новую квоту сразу: он заранее держит открытый запрос к control plane.\n"+
			"Процесс тот же, без перезапуска (pid %d → %d).", pid, d.dp.pid()))
	}

	d.summary()
	return nil
}

func (d *demo) intro() {
	d.say(`
WasmHooks, Milestone 2: «шумный сосед»
──────────────────────────────────────
Платёжная платформа (оператор) спрашивает у WasmHooks скидку для каждого заказа.
Скидку считает маленький скрипт, который написал сам магазин. Скрипты магазина А
и магазина Б работают на одном и том же сервере. Лимит скрипта — 50 мс.

Сервер в демо ограничен 4 ядрами, как эталонный сервер из замеров.
Каждую секунду печатается строка:
  магазин А — сколько заказов, сколько из них сломалось, p99 (99% заказов быстрее этого);
  магазин Б — что случилось с его вызовами.`)
}

func (d *demo) restart(flags []string) error {
	if d.dp != nil {
		d.dp.stop()
	}
	dp, err := startDataPlane(d.exe, d.modulesDir, d.cp, d.procs, flags, d.st.invocation)
	if err != nil {
		return err
	}
	d.dp = dp
	// Warm shop A's instances so the scene starts from a steady state.
	c := newCaller(dp.base, 4)
	for range 50 {
		c.call(context.Background(), shopA)
	}
	return nil
}

func (d *demo) shopATraffic(ctx context.Context, wg *sync.WaitGroup) {
	steady(ctx, wg, newCaller(d.dp.base, 64), shopA, 100, d.st.shopA)
}

// scene runs traffic for the scene's length, printing a line per second.
// each, if set, runs at the start of every second.
func (d *demo) scene(name string, flood bool, traffic func(context.Context, *sync.WaitGroup), each func(sec int)) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	d.st.flush()
	traffic(ctx, &wg)
	if flood {
		d.say("  сек │ магазин А (сосед)                         │ магазин Б (распродажа)")
	} else {
		d.say("  сек │ магазин А (сосед)                         │ магазин Б")
	}
	var total window
	tick := time.NewTicker(time.Second)
	secs := int(d.length / time.Second)
	for sec := 1; sec <= secs; sec++ {
		if each != nil {
			each(sec)
		}
		<-tick.C
		w := d.st.flush()
		total.add(w)
		d.say(fmt.Sprintf("  %3d │ %s │ %s", sec, shopALine(w), shopBLine(w, flood)))
	}
	tick.Stop()
	cancel()
	wg.Wait()
	d.st.flush()
	d.results = append(d.results, result{scene: name, w: total, flood: flood})
	d.say(fmt.Sprintf("  Итог: магазин А — p99 %s, сломалось %d из %d.%s\n", ms(total.p99()), total.aFail, total.aN, shopBSummary(total, flood)))
}

func shopALine(w window) string {
	return fmt.Sprintf("%4d заказов, сломалось %3d, p99 %8s", w.aN, w.aFail, ms(w.p99()))
}

func shopBLine(w window, flood bool) string {
	switch {
	case flood:
		return fmt.Sprintf("принято %4d, отказ по квоте (429) %5d", w.bOK, w.bQuota)
	case w.bTimeout+w.bBusy+w.bQuota+w.bFail == 0:
		return "молчит"
	default:
		late := "—"
		if w.bLateN > 0 {
			late = ms(w.bLate)
		}
		return fmt.Sprintf("остановлено по лимиту %3d, сторож опоздал до %8s, отказ «узел занят» (503) %4d",
			w.bTimeout, late, w.bBusy)
	}
}

func shopBSummary(w window, flood bool) string {
	switch {
	case flood:
		return fmt.Sprintf(" Магазин Б — принято %d, отказ по квоте %d.", w.bOK, w.bQuota)
	case w.bLateN > 0:
		return fmt.Sprintf(" Скрипт Б останавливали с опозданием до %s.", ms(w.bLate))
	}
	return ""
}

func (d *demo) summary() {
	d.say(`
Итоги
─────`)
	d.say("  сцена                              │ магазин А: p99 │ сломалось  │ сторож опоздал │ магазин Б: принято / отказ")
	for _, r := range d.results {
		late, b := "—", "—"
		if r.w.bLateN > 0 {
			late = ms(r.w.bLate)
		}
		switch {
		case r.flood:
			b = fmt.Sprintf("%d / %d", r.w.bOK, r.w.bQuota)
		case r.w.bBusy > 0:
			b = fmt.Sprintf("отказов «узел занят» %d", r.w.bBusy)
		}
		d.say(fmt.Sprintf("  %-34s │ %14s │ %4d / %4d │ %14s │ %s", r.scene, ms(r.w.p99()), r.w.aFail, r.w.aN, late, b))
	}
}

func (d *demo) say(s string) { fmt.Fprintln(d.out, s) }

func (d *demo) pause(title string) {
	d.say("\n" + title)
	if d.auto {
		time.Sleep(time.Second)
		return
	}
	fmt.Fprint(d.out, "  ▶ Enter — начать")
	d.in.ReadString('\n')
}

// ms formats a duration in milliseconds the Russian way: 1,2 мс.
func ms(dur time.Duration) string {
	v := float64(dur) / float64(time.Millisecond)
	var s string
	switch {
	case v >= 100:
		s = fmt.Sprintf("%.0f", v)
	case v >= 10:
		s = fmt.Sprintf("%.1f", v)
	default:
		s = fmt.Sprintf("%.2f", v)
	}
	return strings.Replace(s, ".", ",", 1) + " мс"
}
