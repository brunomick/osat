#ifndef OSAT_ENGINE_LINUX_H
#define OSAT_ENGINE_LINUX_H

#include <gtk/gtk.h>
#include <stdint.h>

// Lo que Go exporta para que lo llame el código C (siempre desde el hilo de GTK).
extern void osatRunIdle(uintptr_t job);
extern void osatEvent(char *id, char *kind, char *url, char *title, int canBack, int canFwd);
extern void osatViewDestroyed(char *id, void *view);

// Todo esto se llama desde el hilo de GTK, salvo osat_post, osat_try_acquire y osat_release.
void osat_post(uintptr_t job);
int osat_try_acquire(void);
void osat_release(void);
GtkWidget *osat_view_new(const char *profileDir, const char *id, const char *url, int x, int y, int w, int h, char **err);
void osat_view_bounds(GtkWidget *view, int x, int y, int w, int h);
void osat_view_visible(GtkWidget *view, int visible);
void osat_view_navigate(GtkWidget *view, const char *url);
void osat_view_back(GtkWidget *view);
void osat_view_forward(GtkWidget *view);
void osat_view_reload(GtkWidget *view);
void osat_view_close(GtkWidget *view);
void osat_release_context(void);

#endif
