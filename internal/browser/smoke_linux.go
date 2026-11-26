//go:build linux && !android && cgo && webkit2_41 && osatsmoke

package browser

// Ayuda para la prueba de humo del motor de Linux (smoke_linux_test.go): los tests no pueden usar cgo, así que la
// ventana de prueba se arma acá. Solo se compila con la etiqueta osatsmoke; osat no la usa.

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

// Una ventana como la de osat: una vista de WebKit adentro (la de Wails), que ensure_overlay encuentra.
static int smoke_init(void) {
    if (!gtk_init_check(NULL, NULL)) return 0;
    GtkWidget *w = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_title(GTK_WINDOW(w), "osat-smoke");
    gtk_window_set_default_size(GTK_WINDOW(w), 900, 700);
    gtk_container_add(GTK_CONTAINER(w), webkit_web_view_new());
    gtk_widget_show_all(w);
    return 1;
}

static void smoke_main(void) { gtk_main(); }

static gboolean smoke_quit_cb(gpointer data) {
    gtk_main_quit();
    return G_SOURCE_REMOVE;
}

static void smoke_quit(void) { g_idle_add(smoke_quit_cb, NULL); }
*/
import "C"

import "runtime"

// smokeGTK arranca GTK con la ventana de prueba y su bucle en un hilo propio. ok es false si no hay pantalla.
func smokeGTK() (stop func(), ok bool) {
	ready := make(chan bool)
	go func() {
		runtime.LockOSThread()
		if C.smoke_init() == 0 {
			ready <- false
			return
		}
		ready <- true
		C.smoke_main()
	}()
	if !<-ready {
		return nil, false
	}
	return func() { C.smoke_quit() }, true
}
