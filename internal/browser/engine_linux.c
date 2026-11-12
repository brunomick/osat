//go:build linux && !android && cgo && webkit2_41

#include "engine_linux.h"

#include <string.h>
#include <webkit2/webkit2.h>

// Motor de Linux: WebKitGTK 4.1. Ver engine_linux.go para cómo se incrusta.

static GtkWidget *overlay = NULL;        // GtkOverlay que envuelve el contenido de la ventana de osat
static GtkWidget *mainView = NULL;       // la vista de Wails: de ahí se miden las posiciones
static WebKitWebContext *webCtx = NULL;  // contexto con el perfil propio

static gboolean idle_cb(gpointer data) {
    osatRunIdle((uintptr_t)data);
    return G_SOURCE_REMOVE;
}

// G_PRIORITY_DEFAULT: que los cambios de posición no queden detrás del dibujo.
void osat_post(uintptr_t job) {
    g_idle_add_full(G_PRIORITY_DEFAULT, idle_cb, (gpointer)job, NULL);
}

// Si el bucle de GTK no está corriendo (osat ya lo cerró), quien llama se queda con el
// contexto y puede usar GTK directo. Si está corriendo, el contexto es del hilo de GTK y falla.
int osat_try_acquire(void) { return g_main_context_acquire(g_main_context_default()); }
void osat_release(void) { g_main_context_release(g_main_context_default()); }

// ---------- Descargas ----------

// Nombre que no pise a otro archivo: "a.zip", "a (1).zip", "a (2).zip"...
static char *unique_path(const char *dir, const char *name) {
    char *path = g_build_filename(dir, name, NULL);
    const char *dot = strrchr(name, '.');
    for (int i = 1; i < 1000 && g_file_test(path, G_FILE_TEST_EXISTS); i++) {
        g_free(path);
        char *stem = (dot && dot != name) ? g_strndup(name, dot - name) : g_strdup(name);
        char *next = g_strdup_printf("%s (%d)%s", stem, i, (dot && dot != name) ? dot : "");
        path = g_build_filename(dir, next, NULL);
        g_free(stem);
        g_free(next);
    }
    return path;
}

static gboolean on_decide_destination(WebKitDownload *download, gchar *suggested, gpointer data) {
    const char *dir = g_get_user_special_dir(G_USER_DIRECTORY_DOWNLOAD);
    if (!dir) dir = g_get_home_dir();
    char *name = g_path_get_basename(suggested && *suggested ? suggested : "download");
    if (!strcmp(name, ".") || !strcmp(name, "..") || !strcmp(name, G_DIR_SEPARATOR_S)) {
        g_free(name);
        name = g_strdup("download");
    }
    char *path = unique_path(dir, name);
    char *uri = g_filename_to_uri(path, NULL, NULL);
    if (uri) webkit_download_set_destination(download, uri);
    g_free(uri);
    g_free(path);
    g_free(name);
    return TRUE;
}

static void on_download_started(WebKitWebContext *ctx, WebKitDownload *download, gpointer data) {
    g_signal_connect(download, "decide-destination", G_CALLBACK(on_decide_destination), NULL);
}

// ---------- Eventos de cada vista ----------

// Manda el estado actual de la vista. uri reemplaza a la dirección actual si no es NULL.
static void emit(GtkWidget *v, const char *kind, const char *uri) {
    WebKitWebView *wv = WEBKIT_WEB_VIEW(v);
    const char *id = g_object_get_data(G_OBJECT(v), "osat-id");
    const char *title = webkit_web_view_get_title(wv);
    if (!uri) uri = webkit_web_view_get_uri(wv);
    osatEvent((char *)id, (char *)kind, (char *)(uri ? uri : ""), (char *)(title ? title : ""),
              webkit_web_view_can_go_back(wv), webkit_web_view_can_go_forward(wv));
}

static void on_uri(GObject *o, GParamSpec *p, gpointer data) { emit(GTK_WIDGET(o), "nav", NULL); }
static void on_title(GObject *o, GParamSpec *p, gpointer data) { emit(GTK_WIDGET(o), "title", NULL); }
static void on_history(WebKitBackForwardList *l, WebKitBackForwardListItem *added, gpointer removed, gpointer view) {
    emit(GTK_WIDGET(view), "nav", NULL);
}

// Ventana nueva (target=_blank, window.open): no se abre nada, lo resuelve la pestaña.
static GtkWidget *on_create(WebKitWebView *wv, WebKitNavigationAction *action, gpointer data) {
    WebKitURIRequest *req = webkit_navigation_action_get_request(action);
    const char *uri = req ? webkit_uri_request_get_uri(req) : NULL;
    emit(GTK_WIDGET(wv), "newWindow", uri ? uri : "");
    return NULL;
}

static gboolean on_load_failed(WebKitWebView *wv, WebKitLoadEvent ev, gchar *uri, GError *err, gpointer data) {
    // Cancelar una carga o que una descarga interrumpa la página no es un error de la página.
    if (err && ((err->domain == WEBKIT_NETWORK_ERROR && err->code == WEBKIT_NETWORK_ERROR_CANCELLED) ||
                err->domain == WEBKIT_POLICY_ERROR))
        return FALSE;
    emit(GTK_WIDGET(wv), "error", uri);
    return FALSE;
}

static void on_view_destroy(GtkWidget *v, gpointer data) {
    osatViewDestroyed((char *)g_object_get_data(G_OBJECT(v), "osat-id"), v);
}

// ---------- Ventana ----------

static void find_webview(GtkWidget *w, gpointer data) {
    GtkWidget **found = data;
    if (*found) return;
    if (WEBKIT_IS_WEB_VIEW(w)) {
        *found = w;
    } else if (GTK_IS_CONTAINER(w)) {
        gtk_container_forall(GTK_CONTAINER(w), find_webview, data);
    }
}

// Cada vista nuestra se ubica con el rectángulo que guarda (relativo a la vista de Wails).
static gboolean on_child_position(GtkOverlay *o, GtkWidget *child, GdkRectangle *alloc, gpointer data) {
    GdkRectangle *r = g_object_get_data(G_OBJECT(child), "osat-rect");
    if (!r) return FALSE;
    int ox = 0, oy = 0;
    // Si Wails puso algo arriba de su vista (una barra de menú), la posición baja lo mismo.
    if (!mainView || !gtk_widget_translate_coordinates(mainView, GTK_WIDGET(o), 0, 0, &ox, &oy)) ox = oy = 0;
    alloc->x = r->x + ox;
    alloc->y = r->y + oy;
    alloc->width = r->width > 0 ? r->width : 1;
    alloc->height = r->height > 0 ? r->height : 1;
    return TRUE;
}

static void on_overlay_destroy(GtkWidget *w, gpointer data) {
    overlay = NULL;
    mainView = NULL;
}

// Mete el contenido de la ventana de osat dentro de un GtkOverlay, una sola vez. Las vistas
// de los sitios van como hijos del overlay, por encima de todo lo demás.
static const char *ensure_overlay(void) {
    if (overlay) return NULL;
    GtkWidget *found = NULL;
    GtkWindow *win = NULL;
    GList *tops = gtk_window_list_toplevels();
    for (GList *i = tops; i && !win; i = i->next) {
        GtkWindow *w = GTK_WINDOW(i->data);
        if (gtk_window_get_window_type(w) != GTK_WINDOW_TOPLEVEL || !gtk_bin_get_child(GTK_BIN(w))) continue;
        found = NULL;
        find_webview(GTK_WIDGET(w), &found);
        if (found) win = w;
    }
    g_list_free(tops);
    if (!win) return "browser: no hay ventana principal";

    GtkWidget *content = gtk_bin_get_child(GTK_BIN(win));
    g_object_ref(content);
    gtk_container_remove(GTK_CONTAINER(win), content);
    overlay = gtk_overlay_new();
    gtk_container_add(GTK_CONTAINER(overlay), content);
    g_object_unref(content);
    gtk_container_add(GTK_CONTAINER(win), overlay);
    mainView = found;
    g_signal_connect(overlay, "get-child-position", G_CALLBACK(on_child_position), NULL);
    g_signal_connect(overlay, "destroy", G_CALLBACK(on_overlay_destroy), NULL);
    gtk_widget_show(overlay);
    return NULL;
}

// Crea el contexto de WebKit con el perfil propio: datos (localStorage, IndexedDB...) y cookies
// en profileDir, así el login sigue después de reiniciar.
static const char *ensure_context(const char *profileDir) {
    if (webCtx) return NULL;
    char *cache = g_build_filename(profileDir, "cache", NULL);
    WebKitWebsiteDataManager *dm = webkit_website_data_manager_new(
        "base-data-directory", profileDir, "base-cache-directory", cache, NULL);
    g_free(cache);
    if (!dm) return "browser: no se pudo crear el perfil";
    char *cookies = g_build_filename(profileDir, "cookies.sqlite", NULL);
    webkit_cookie_manager_set_persistent_storage(webkit_website_data_manager_get_cookie_manager(dm), cookies,
                                                 WEBKIT_COOKIE_PERSISTENT_STORAGE_SQLITE);
    g_free(cookies);
    webCtx = webkit_web_context_new_with_website_data_manager(dm);
    g_object_unref(dm);
    g_signal_connect(webCtx, "download-started", G_CALLBACK(on_download_started), NULL);
    return NULL;
}

// ---------- Vistas ----------

static void set_rect(GtkWidget *v, int x, int y, int w, int h) {
    GdkRectangle *r = g_object_get_data(G_OBJECT(v), "osat-rect");
    if (!r) return;
    r->x = x;
    r->y = y;
    r->width = w;
    r->height = h;
    gtk_widget_queue_resize(v);
}

GtkWidget *osat_view_new(const char *profileDir, const char *id, const char *url, int x, int y, int w, int h, char **err) {
    const char *msg = ensure_context(profileDir);
    if (!msg) msg = ensure_overlay();
    if (msg) {
        *err = g_strdup(msg);
        return NULL;
    }
    // Vista propia, con su contexto: no tiene el esquema wails:// ni ningún manejador de mensajes,
    // así que la página no puede hablarle a osat.
    GtkWidget *v = webkit_web_view_new_with_context(webCtx);
    g_object_set_data_full(G_OBJECT(v), "osat-id", g_strdup(id), g_free);
    g_object_set_data_full(G_OBJECT(v), "osat-rect", g_new0(GdkRectangle, 1), g_free);
    set_rect(v, x, y, w, h);

    g_signal_connect(v, "notify::uri", G_CALLBACK(on_uri), NULL);
    g_signal_connect(v, "notify::title", G_CALLBACK(on_title), NULL);
    g_signal_connect(webkit_web_view_get_back_forward_list(WEBKIT_WEB_VIEW(v)), "changed", G_CALLBACK(on_history), v);
    g_signal_connect(v, "create", G_CALLBACK(on_create), NULL);
    g_signal_connect(v, "load-failed", G_CALLBACK(on_load_failed), NULL);
    g_signal_connect(v, "destroy", G_CALLBACK(on_view_destroy), NULL);

    gtk_overlay_add_overlay(GTK_OVERLAY(overlay), v);
    gtk_widget_show(v);
    webkit_web_view_load_uri(WEBKIT_WEB_VIEW(v), url);
    return v;
}

void osat_view_bounds(GtkWidget *v, int x, int y, int w, int h) { set_rect(v, x, y, w, h); }
void osat_view_visible(GtkWidget *v, int visible) { gtk_widget_set_visible(v, visible); }
void osat_view_navigate(GtkWidget *v, const char *url) { webkit_web_view_load_uri(WEBKIT_WEB_VIEW(v), url); }
void osat_view_back(GtkWidget *v) { webkit_web_view_go_back(WEBKIT_WEB_VIEW(v)); }
void osat_view_forward(GtkWidget *v) { webkit_web_view_go_forward(WEBKIT_WEB_VIEW(v)); }
void osat_view_reload(GtkWidget *v) { webkit_web_view_reload(WEBKIT_WEB_VIEW(v)); }
void osat_view_close(GtkWidget *v) { gtk_widget_destroy(v); }

// Suelta el contexto: sin vistas ni contexto, WebKit termina su proceso de red y guarda las cookies.
// La próxima vista lo vuelve a crear.
void osat_release_context(void) {
    if (webCtx) {
        g_object_unref(webCtx);
        webCtx = NULL;
    }
}
