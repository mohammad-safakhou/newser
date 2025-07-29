document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('create-topic-form');
  const titleInput = document.getElementById('topic-title');
  const list = document.getElementById('topics-list');

  async function loadTopics() {
    list.innerHTML = '';
    try {
      const res = await fetch('/topics');
      const topics = await res.json();
      topics.forEach(topic => {
        const li = document.createElement('li');
        const a = document.createElement('a');
        a.href = `/topic.html?title=${encodeURIComponent(topic.title)}`;
        a.textContent = topic.title;
        li.appendChild(a);
        list.appendChild(li);
      });
    } catch (err) {
      console.error('Error loading topics', err);
    }
  }

  form.addEventListener('submit', async e => {
    e.preventDefault();
    const title = titleInput.value.trim();
    if (!title) return;
    try {
      await fetch('/topics', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({title})
      });
      titleInput.value = '';
      loadTopics();
    } catch (err) {
      console.error('Error creating topic', err);
    }
  });

  loadTopics();
});
