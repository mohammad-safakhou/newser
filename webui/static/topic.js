document.addEventListener('DOMContentLoaded', async () => {
  const params = new URLSearchParams(window.location.search);
  const title = params.get('title');
  if (!title) return;
  document.getElementById('topic-title').textContent = title;

  const historyDiv = document.getElementById('history');
  const messageInput = document.getElementById('message');
  const form = document.getElementById('modify-topic-form');
  const newsBtn = document.getElementById('generate-news');
  const newsOutput = document.getElementById('news-output');

  async function loadTopic() {
    historyDiv.innerHTML = '';
    try {
      const res = await fetch(`/topics/${encodeURIComponent(title)}`);
      const topic = await res.json();
      topic.history.forEach(item => {
        const p = document.createElement('p');
        p.textContent = item;
        historyDiv.appendChild(p);
      });
    } catch (err) {
      console.error('Error loading topic', err);
    }
  }

  form.addEventListener('submit', async e => {
    e.preventDefault();
    try {
      const res = await fetch(`/topics/${encodeURIComponent(title)}`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({message: messageInput.value.trim()})
      });
      const data = await res.json();
      messageInput.value = '';
      loadTopic();
    } catch (err) {
      console.error('Error modifying topic', err);
    }
  });

  newsBtn.addEventListener('click', async () => {
    try {
      const res = await fetch(`/topics/${encodeURIComponent(title)}/generate-news`);
      const data = await res.json();
      newsOutput.textContent = data.news;
    } catch (err) {
      console.error('Error generating news', err);
    }
  });

  loadTopic();
});
