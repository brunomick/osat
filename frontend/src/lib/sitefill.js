// Rellena el login de la página con una credencial de los sitios. Lo embebe Go (app_sites.go, BrowserFill) y
// lo corre en el documento principal de la página del sitio como `(<este archivo>)({domain, account, user,
// password})`; el frontend no lo usa. Tiene que ser una sola expresión que dé la función.
// Devuelve "ok" (rellenó algo), "host" (la página no es del dominio de la credencial: no toca nada) o "form"
// (no encontró dónde). No envía el formulario.
// Con {probe: true} solo mira: devuelve "login" si hay dónde escribir (contraseña, usuario o cuenta) o "none", sin
// comprobar el dominio ni tocar nada (para ofrecer el panel de credenciales). Con {auto: true} (relleno sin preguntar)
// no pisa un campo que ya tenga algo escrito.
(function (d) {
  var host = location.hostname.toLowerCase()
  if (!d.probe && (!d.domain || (host !== d.domain && host.slice(-d.domain.length - 1) !== '.' + d.domain))) return 'host'

  function type(e) {
    return (e.getAttribute('type') || 'text').toLowerCase()
  }
  function hint(e) {
    return [e.name, e.id, e.getAttribute('autocomplete'), e.getAttribute('placeholder'), e.getAttribute('aria-label')]
      .join(' ')
      .toLowerCase()
  }
  var inputs = Array.prototype.slice.call(document.querySelectorAll('input')).filter(function (e) {
    return !e.disabled && !e.readOnly && e.getClientRects().length > 0
  })
  var password = inputs.filter(function (e) {
    return type(e) === 'password'
  })[0]
  var form = password && password.form
  var texts = inputs.filter(function (e) {
    var t = type(e)
    return (t === 'text' || t === 'email' || t === 'tel' || t === 'number') && (!form || e.form === form)
  })

  var USER = /user|login|e-?mail|usuario|correo|identifier/
  var ACCOUNT = /account|tenant|company|organi[sz]ation|cuenta|empresa|organizaci/
  function isUser(e) {
    var a = (e.getAttribute('autocomplete') || '').toLowerCase()
    return a === 'username' || a === 'email' || type(e) === 'email' || USER.test(hint(e))
  }
  var account = texts.filter(function (e) {
    return ACCOUNT.test(hint(e)) && (e.getAttribute('autocomplete') || '').toLowerCase() !== 'username'
  })[0]
  var rest = texts.filter(function (e) {
    return e !== account
  })
  var user = rest.filter(isUser)[0]
  if (!user && password) {
    // Sin pistas: el último campo de texto antes de la contraseña.
    var before = rest.filter(function (e) {
      return e.compareDocumentPosition(password) & Node.DOCUMENT_POSITION_FOLLOWING
    })
    user = before[before.length - 1]
  }

  if (d.probe) return password || user || account ? 'login' : 'none'

  // Con el setter nativo y los eventos, los frameworks (React, Angular…) ven el cambio.
  var setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
  var n = 0
  function put(e, v) {
    if (!e || !v || (d.auto && e.value)) return
    e.focus()
    setValue.call(e, v)
    e.dispatchEvent(new Event('input', { bubbles: true }))
    e.dispatchEvent(new Event('change', { bubbles: true }))
    n++
  }
  put(account, d.account)
  put(user, d.user)
  put(password, d.password)
  return n > 0 ? 'ok' : 'form'
})
