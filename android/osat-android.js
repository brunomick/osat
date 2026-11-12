// Puente de osat en Android: arma window.go.main.App y window.runtime (lo que en escritorio pone
// Wails) sobre el servidor local de main_android.go. Lo nativo de Android (portapapeles, enlaces,
// elegir archivos) va por window.OsatAndroid, que agrega la app en MainActivity.kt.
;(function () {
  var native = window.OsatAndroid

  function call(method, args) {
    return fetch('/call', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ m: method, a: args }),
    })
      .then(function (r) {
        return r.json()
      })
      .then(function (res) {
        // Como Wails: un error rechaza con su texto (api.ts busca ahí los "OSAT:{json}").
        if (res.e !== undefined) throw res.e
        return res.r
      })
  }

  // Cada llamada es un fetch aparte y pueden llegar en otro orden: lo que se escribe en una
  // terminal va en fila por sesión, para que las teclas no se mezclen.
  var termQueues = {}
  var App = new Proxy(
    {},
    {
      get: function (_, name) {
        return function () {
          var args = Array.prototype.slice.call(arguments)
          if (name !== 'TermWrite') return call(name, args)
          var prev = termQueues[args[0]] || Promise.resolve()
          var next = prev.catch(function () {}).then(function () {
            return call(name, args)
          })
          termQueues[args[0]] = next
          return next
        }
      },
    },
  )
  window.go = { main: { App: App } }

  // ---- eventos ----
  var listeners = {}
  function dispatch(name, data) {
    var list = listeners[name]
    if (!list) return
    list.slice().forEach(function (cb) {
      cb.apply(null, data)
    })
  }
  function connect() {
    var ws = new WebSocket('ws://' + location.host + '/events')
    ws.onmessage = function (ev) {
      var msg = JSON.parse(ev.data)
      if (msg.n === 'android:pick') {
        if (native) native.pickFiles(msg.d[0], msg.d[1])
        else window.__osatPicked(msg.d[0], [])
        return
      }
      if (msg.n === 'android:open') {
        if (native) native.openUrl(msg.d[0])
        return
      }
      dispatch(msg.n, msg.d)
    }
    ws.onclose = function () {
      setTimeout(connect, 500)
    }
  }
  connect()

  // La app llama acá cuando terminó de elegir archivos (paths vacío = cancelado).
  window.__osatPicked = function (id, paths) {
    fetch('/picked', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: id, paths: paths }),
    })
  }

  var runtime = {
    EventsOnMultiple: function (name, cb) {
      ;(listeners[name] = listeners[name] || []).push(cb)
      return function () {
        listeners[name] = (listeners[name] || []).filter(function (x) {
          return x !== cb
        })
      }
    },
    EventsOn: function (name, cb) {
      return runtime.EventsOnMultiple(name, cb, -1)
    },
    EventsOff: function (name) {
      delete listeners[name]
    },
    Environment: function () {
      return Promise.resolve({ platform: 'linux', arch: 'arm64', buildType: 'production' })
    },
    ClipboardGetText: function () {
      return Promise.resolve(native ? native.clipboardGet() : '')
    },
    ClipboardSetText: function (text) {
      if (native) native.clipboardSet(text)
      return Promise.resolve(true)
    },
    BrowserOpenURL: function (url) {
      if (native) native.openUrl(url)
    },
    WindowIsMaximised: function () {
      return Promise.resolve(true)
    },
  }
  // El resto del runtime de Wails (ventana, arrastrar archivos…) no tiene sentido en Android.
  window.runtime = new Proxy(runtime, {
    get: function (target, name) {
      if (name in target) return target[name]
      return function () {
        return Promise.resolve()
      }
    },
  })
  document.documentElement.classList.add('android')
})()
