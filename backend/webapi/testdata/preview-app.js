import { label } from "/src/helper.js";

const get = await (await fetch("http://localhost:API_PORT/api")).json();
const omit = await (
  await fetch("http://localhost:API_PORT/api?omit=1", { credentials: "omit" })
).json();
const post = await (
  await fetch(
    new Request("http://127.0.0.1:API_PORT/echo", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ posted: true }),
    }),
  )
).json();
const relative = await (await fetch("/ping")).text();
const xhr = await new Promise((resolve, reject) => {
  const request = new XMLHttpRequest();
  request.open("GET", "http://127.0.0.1:API_PORT/api");
  request.onload = () => resolve(JSON.parse(request.responseText));
  request.onerror = reject;
  request.send();
});
const event = await new Promise((resolve, reject) => {
  const source = new EventSource("http://localhost:API_PORT/events");
  source.onmessage = (event) => {
    source.close();
    resolve(event.data);
  };
  source.onerror = reject;
});
const websocket = await new Promise((resolve, reject) => {
  const socket = new WebSocket("ws://127.0.0.1:API_PORT/socket");
  socket.onopen = () => socket.send("hot-reload");
  socket.onmessage = (event) => {
    socket.close();
    resolve(event.data);
  };
  socket.onerror = reject;
});
localStorage.setItem("preview-app-storage", "works");
document.cookie = "zentrale_session=changed-app; Path=/";
document.cookie = "js_cookie=works; Path=/";
document.cookie = "hidden_cookie=scoped; Path=/other";
const cookies = await (await fetch("http://localhost:API_PORT/cookies")).json();
window.previewCookies = document.cookie;
window.previewResults = {
  get,
  omit,
  post,
  relative,
  xhr,
  event,
  websocket,
  cookies,
};
document.querySelector("h1").textContent = label;
