# todo ✓

Lista de pendientes personal, minimalista, mobile-first, para un solo usuario. Un solo binario en Go (stdlib + `lib/pq`), plantillas y estáticos embebidos, PostgreSQL, PWA con tema oscuro que **funciona sin conexión**. Hermana de [oink](https://github.com/jesarx/oink): misma lógica de despliegue, misma manera de entrar, otro dominio y otra base.

## La lógica

- **Secciones**: las pestañas de arriba (por defecto Personal, Trabajo y Casa). Se renombran, se reordenan y se borran desde Ajustes; hasta 6 y mínimo una. Al borrar una, sus tareas se mudan a la primera en vez de perderse.
- **Tareas**: título y, si hace falta, notas. No hay fechas límite a propósito: cada cosa se hace cuando se puede. Sí se registra **cuándo se agregó y cuándo se terminó** cada una; las hechas se guardan con su fecha de término en un desplegable al final de la lista.
- **Todas**: los pendientes de todas las secciones de un vistazo, una tarjeta por sección (en el celular una tras otra, en la computadora en columnas). Se pueden marcar y abrir sus notas ahí mismo.
- **Orden**: primero las fijadas (★) y luego lo más reciente arriba, que es lo que uno acaba de anotar.
- **Notas con formato**: dentro de una tarea puedes escribir listas y se formatean solas.

  ```
  * viñetas          (también - o +)
  1. numeradas
  [ ] casillas       (se marcan con un toque, sin abrir la tarea)
  [x] ya marcada
  **negritas**, _cursivas_, `código`, y las ligas se vuelven enlaces
  ```

  Las listas **se siguen solas**: al dar Enter dentro de una, el renglón nuevo nace con la misma marca (y las numeradas avanzan solas). Un segundo Enter sobre el renglón vacío quita la marca y regresa al texto normal, como en las apps de mensajería. Shift+Enter siempre da un salto de renglón limpio.

  Todo se escapa antes de pintarse: una nota nunca puede inyectar HTML.
- **Sin conexión**: la app abre con la última copia vista de cada página, y lo que agregues o marques sin red se guarda en una cola local que se sincroniza sola al volver la señal, en orden. Cada alta lleva un id único, así que un reintento nunca duplica; marcar o borrar algo que aún no se sube también funciona (viaja por su id local). La barra amarilla de arriba dice cuántos cambios faltan por subir.
- **Búsqueda** en tareas y notas, abiertas y hechas.
- **CSV** con todo (sección, tarea, notas, fijada, agregada, terminada) desde Ajustes.

## Requisitos

- Go 1.22+ (solo para compilar; en producción corre el binario)
- PostgreSQL
- nginx + certbot

## Despliegue en el VPS

### 1. Base de datos

```bash
sudo -u postgres psql
CREATE USER todo_user WITH PASSWORD 'un_password_seguro';
CREATE DATABASE todo OWNER todo_user;
\q
```

El esquema se crea solo al arrancar (migración automática con `IF NOT EXISTS`).

### 2. Compilar

```bash
sudo mkdir -p /var/www/todo && sudo chown $USER:$USER /var/www/todo
git clone <tu-repo>/todo.git /var/www/todo && cd /var/www/todo
go build -o todo .
```

El repo debe ser **tuyo**, no de root: si lo clonas con `sudo`, git ve un dueño ajeno y el build falla con `error obtaining VCS status: exit status 128`. Si ya te pasó, se arregla con `sudo chown -R $USER:$USER /var/www/todo` (o compila una vez con `go build -buildvcs=false -o todo .`). El binario queda 755, que es todo lo que `www-data` necesita para ejecutarlo.

(O compila en tu ThinkPad con `GOOS=linux GOARCH=amd64 go build -o todo .` y sube solo el binario con scp; todo va embebido.)

### 3. Contraseña y entorno

```bash
./todo hash          # escribe tu contraseña, copia el hash resultante
sudo vim /etc/todo.env
```

Contenido de `/etc/todo.env`:

```
TODO_DSN=postgres://todo_user:un_password_seguro@localhost/todo?sslmode=disable
TODO_PASSWORD_HASH=pbkdf2-sha256$300000$...$...
TODO_ADDR=127.0.0.1:4200
TODO_TZ=America/Mexico_City
```

```bash
sudo chmod 600 /etc/todo.env
```

El puerto 4200 no choca con oink (4100); pueden convivir en el mismo VPS.

### 4. systemd y nginx

```bash
sudo cp deploy/todo.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now todo
sudo journalctl -u todo -n 20   # debe decir "todo escuchando en 127.0.0.1:4200"

sudo cp deploy/nginx-todo.conf /etc/nginx/sites-available/todo
sudo ln -s /etc/nginx/sites-available/todo /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d todo.qumran.cc
```

No olvides el registro DNS `A` para `todo.qumran.cc` apuntando al VPS.

### 5. En el celular

Abre `https://todo.qumran.cc`, entra con tu contraseña, y en el menú del navegador elige **"Agregar a pantalla de inicio"**. Queda instalada como app con la palomita de ícono y abre a pantalla completa. La sesión dura 90 días y se renueva sola mientras la uses.

> El certificado (HTTPS) es necesario: sin él el navegador no instala la app ni deja trabajar sin conexión.

## En la computadora

Mismo sitio, mismo login. Atajos: `/` o `n` saltan al campo de nueva tarea, `Esc` lo suelta.

## Seguridad

- Sin npm ni Node en producción; una sola dependencia externa (`lib/pq`, driver puro).
- Contraseña con PBKDF2-HMAC-SHA256 (300,000 iteraciones), comparación en tiempo constante.
- Sesiones opacas en Postgres, cookie `Secure` + `HttpOnly` + `SameSite=Strict`.
- Rate limiting: 5 intentos fallidos → bloqueo de 15 minutos.
- Verificación de `Origin` en todos los POST (defensa CSRF adicional).
- CSP estricta, `X-Frame-Options: DENY`, `nosniff`, cuerpo de POST acotado a 64 KB.
- systemd con sandbox (`ProtectSystem=strict`, `NoNewPrivileges`, etc.).

## Backup

Agrega a tu script de respaldos existente:

```bash
pg_dump -Fc -U todo_user todo > backup_todo.dump
```

## Actualizar

```bash
cd /var/www/todo && git pull && go build -o todo . && sudo systemctl restart todo
```

Las migraciones corren solas al arrancar. La versión que quitó las fechas límite borra de paso la columna `due_on`; las tareas, sus notas y sus fechas de alta y término no se tocan.

## Desarrollo

```bash
go test ./...        # formato de notas, casillas y redirecciones
go run ./tools/icon  # regenera los PNG del ícono desde el mismo dibujo del favicon
```
